// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

type Config struct {
	StaticURL  string	// Expect e.g. "/static"
	StaticPath string	// Local path with static files
}