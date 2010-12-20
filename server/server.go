// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bufio"
	"container/list"
	"net"
	"os"
	"sync"
	"time"
	. "github.com/petar/GoHTTP/http"
	. "github.com/petar/GoHTTP/util"
)

// Server automates the reception of incoming HTTP connections
// at a given net.Listener. Server accepts new connections and
// manages each one with an ServerConn object. Server also
// makes sure that a pre-specified limit of active connections (i.e.
// file descriptors) is not exceeded.
type Server struct {
	tmo    int64 // keepalive timout
	listen net.Listener
	conns  map[*stampedServerConn]int
	qch    chan *Query
	fdl    FDLimiter
	lk     sync.Mutex
}

type stampedServerConn struct {
	*ServerConn
	stamp int64
	lk    sync.Mutex
}

func newStampedServerConn(c net.Conn, r *bufio.Reader) *stampedServerConn {
	return &stampedServerConn{
		ServerConn: NewServerConn(c, r),
		stamp:      time.Nanoseconds(),
	}
}

func (ssc *stampedServerConn) touch() {
	ssc.lk.Lock()
	defer ssc.lk.Unlock()
	ssc.stamp = time.Nanoseconds()
}

func (ssc *stampedServerConn) GetStamp() int64 {
	ssc.lk.Lock()
	defer ssc.lk.Unlock()
	return ssc.stamp
}

func (ssc *stampedServerConn) Read() (req *Request, err os.Error) {
	ssc.touch()
	defer ssc.touch()
	return ssc.ServerConn.Read()
}

func (ssc *stampedServerConn) Write(req *Request, resp *Response) (err os.Error) {
	ssc.touch()
	defer ssc.touch()
	return ssc.ServerConn.Write(req, resp)
}

// NewServer creates a new Server which listens for connections on l.
// New connections are automatically managed by ServerConn objects with
// timout set to tmo nanoseconds. The Server object ensures that at no
// time more than fdlim file descriptors are allocated to incoming connections.
func NewServer(l net.Listener, tmo int64, fdlim int) *Server {
	if tmo < 2 {
		panic("timeout too small")
	}
	// TODO(petar): Perhaps a better design passes the FDLimiter as a parameter
	srv := &Server{
		tmo:    tmo,
		listen: l,
		conns:  make(map[*stampedServerConn]int),
		qch:    make(chan *Query),
	}
	srv.fdl.Init(fdlim)
	go srv.acceptLoop()
	go srv.expireLoop()
	return srv
}

func (srv *Server) GetFDLimiter() *FDLimiter { return &srv.fdl }

func (srv *Server) expireLoop() {
	for {
		srv.lk.Lock()
		if srv.listen == nil {
			srv.lk.Unlock()
			return
		}
		now := time.Nanoseconds()
		kills := list.New()
		for ssc, _ := range srv.conns {
			if now-ssc.GetStamp() >= srv.tmo {
				kills.PushBack(ssc)
			}
		}
		srv.lk.Unlock()
		elm := kills.Front()
		for elm != nil {
			ssc := elm.Value.(*stampedServerConn)
			srv.bury(ssc)
			elm = elm.Next()
		}
		kills.Init()
		kills = nil
		time.Sleep(srv.tmo)
	}
}

func (srv *Server) acceptLoop() {
	for {
		srv.lk.Lock()
		l := srv.listen
		srv.lk.Unlock()
		if l == nil {
			return
		}
		srv.fdl.Lock()
		c, err := l.Accept()
		if err != nil {
			if c != nil {
				c.Close()
			}
			srv.fdl.Unlock()
			srv.qch <- newQueryErr(err)
			return
		}
		c.(*net.TCPConn).SetKeepAlive(true)
		err = c.SetReadTimeout(srv.tmo)
		if err != nil {
			c.Close()
			srv.fdl.Unlock()
			srv.qch <- newQueryErr(err)
			return
		}
		c = NewRunOnCloseConn(c, func() { srv.fdl.Unlock() })
		ssc := newStampedServerConn(c, nil)
		ok := srv.register(ssc)
		if !ok {
			ssc.Close()
			c.Close()
		}
		go srv.read(ssc)
	}
}

// Read() waits until a new request is received. The request is
// returned in the form of a Query object. A returned error
// indicates that the Server cannot accept new connections,
// and the user us expected to call Shutdown(), perhaps after serving
// outstanding queries.
func (srv *Server) Read() (query *Query, err os.Error) {
	q := <-srv.qch
	srv.lk.Lock()
	if closed(srv.qch) {
		srv.lk.Unlock()
		return nil, os.EBADF
	}
	srv.lk.Unlock()
	if err = q.getError(); err != nil {
		return nil, err
	}
	return q, nil
}

func (srv *Server) read(ssc *stampedServerConn) {
	for {
		req, err := ssc.Read()
		perr, ok := err.(*os.PathError)
		if ok && perr.Error == os.EAGAIN {
			srv.bury(ssc)
			return
		}
		if err != nil {
			// TODO(petar): Technically, a read side error should not terminate
			// the ServerConn if there are outstanding requests to be answered,
			// since the write side might still be healthy. But this is
			// virtually never the case with TCP, so we currently go for simplicity
			// and just close the connection.
			srv.bury(ssc)
			return
		}
		srv.qch <- &Query{srv, ssc, req, nil, false, false}
		return
	}
}

func (srv *Server) register(ssc *stampedServerConn) bool {
	srv.lk.Lock()
	defer srv.lk.Unlock()
	if closed(srv.qch) {
		return false
	}
	if _, present := srv.conns[ssc]; present {
		panic("register twice")
	}
	srv.conns[ssc] = 1
	return true
}

func (srv *Server) unregister(ssc *stampedServerConn) {
	srv.lk.Lock()
	defer srv.lk.Unlock()
	srv.conns[ssc] = 0, false
}

func (srv *Server) bury(ssc *stampedServerConn) {
	srv.unregister(ssc)
	c, _, _ := ssc.Close()
	if c != nil {
		c.Close()
	}
}

// Shutdown closes the Server by closing the underlying
// net.Listener object. The user should not use any Server
// or Query methods after a call to Shutdown.
func (srv *Server) Shutdown() (err os.Error) {
	// First, close the listener
	srv.lk.Lock()
	var l net.Listener
	l, srv.listen = srv.listen, nil
	close(srv.qch)
	srv.lk.Unlock()
	if l != nil {
		err = l.Close()
	}
	// Then, force-close all open connections
	srv.lk.Lock()
	for ssc, _ := range srv.conns {
		c, _, _ := ssc.Close()
		if c != nil {
			c.Close()
		}
		srv.conns[ssc] = 0, false
	}
	srv.lk.Unlock()
	return
}