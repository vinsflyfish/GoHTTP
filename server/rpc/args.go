// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"os"
	"github.com/petar/GoHTTP/http"
)

var (
	ErrArg = os.NewError("bad or missing RPC argument")
)

// Args is the argument structure for incoming RPC calls.
type Args struct {
	// Method is the HTTP method used for this request
	Method  string

	// Cookies holds the cookies included in the request
	Cookies []*http.Cookie

	// Query holds the decoded arguments from the request's URL
	Query   map[string][]string

	// Body is the generic JSON-decoded version of the request body, or an empty map otherwise
	Body    map[string]interface{}
}

func (a *Args) QueryBool(key string) (bool, os.Error) {
	if a.Query == nil {
		return false, ErrArg
	}
	v, ok := a.Query[key]
	if !ok || len(v) == 0 {
		return false, ErrArg
	}
	if v[0] == "0" {
		return false, nil
	}
	if v[0] == "1" {
		return true, nil
	}
	return false, ErrArg
}

func (a *Args) QueryString(key string) (string, os.Error) {
	if a.Query == nil {
		return "", ErrArg
	}
	v, ok := a.Query[key]
	if !ok || len(v) == 0 {
		return "", ErrArg
	}
	return v[0], nil
}

// Ret is the return valyes structure of RPC calls
type Ret struct {
	SetCookies []*http.Cookie
	Value      map[string]interface{}
}

func (r *Ret) initIfZero() {
	if r.Value == nil {
		r.Value = make(map[string]interface{})
	}
}

func (r *Ret) SetBool(key string, value bool) {
	r.initIfZero()
	s := "0"
	if value {
		s = "1"
	}
	r.Value[key] = s
}

func (r *Ret) SetInt(key string, value int) {
	r.initIfZero()
	r.Value[key] = value
}

func (r *Ret) SetString(key string, value string) {
	r.initIfZero()
	r.Value[key] = value
}

func (r *Ret) SetInterface(key string, value interface{}) {
	r.initIfZero()
	r.Value[key] = value
}

func (r *Ret) AddSetCookie(setCookie *http.Cookie) {
	r.initIfZero()
	r.SetCookies = append(r.SetCookies, setCookie)
}
