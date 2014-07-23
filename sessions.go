// Copyright 2012 The Gorilla Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sessions

import (
	"encoding/gob"
	"fmt"
	"github.com/frogprog/floki"
	"net/http"
	"time"
)

// Default flashes key.
const flashesKey = "_flash"

// Options --------------------------------------------------------------------

// Options stores configuration for a session or session store.
//
// Fields are a subset of http.Cookie fields.
type Options struct {
	Path   string
	Domain string
	// MaxAge=0 means no 'Max-Age' attribute specified.
	// MaxAge<0 means delete cookie now, equivalently 'Max-Age: 0'.
	// MaxAge>0 means Max-Age attribute present and given in seconds.
	MaxAge   int
	Secure   bool
	HttpOnly bool
}

func flushSession(c *floki.Context) {
	s := c.MustGet("_session").(*Session)

	if s.dirty {
		err := s.Save(c)
		if err != nil {
			c.Logger().Fatalln("error saving session:", err)
		}
	}
}

// Sessions is a Middleware that maps a session.Session service into the Floki handler chain.
// Sessions can use a number of storage solutions with the given store.
func Sessions(name string, store Store, options *Options) floki.HandlerFunc {
	if options == nil {
		options = &Options{
			Path:     "/",
			MaxAge:   3600,
			Secure:   false,
			HttpOnly: true,
		}
	}

	return func(c *floki.Context) {
		// Map to the Session interface
		s, err := GetRegistry(c).Get(store, name)
		if err != nil {
			panic(err)
		}

		c.Set("_session", s)
		// export session values to the request context
		c.Set("session", s.Values)

		c.BeforeDestroy(flushSession)

		c.Next()

	}
}

func Get(c *floki.Context) *Session {
	return c.MustGet("_session").(*Session)
}

// Session --------------------------------------------------------------------

// NewSession is called by session stores to create a new session instance.
func NewSession(store Store, name string) *Session {
	return &Session{
		Values: make(map[interface{}]interface{}),
		store:  store,
		name:   name,
	}
}

// Session stores the values and optional configuration for a session.
type Session struct {
	ID      string
	Values  map[interface{}]interface{}
	Options *Options
	IsNew   bool
	store   Store
	name    string
	dirty   bool
}

// Flashes returns a slice of flash messages from the session.
//
// A single variadic argument is accepted, and it is optional: it defines
// the flash key. If not defined "_flash" is used by default.
func (s *Session) Flashes(vars ...string) []interface{} {
	var flashes []interface{}
	key := flashesKey
	if len(vars) > 0 {
		key = vars[0]
	}
	if v, ok := s.Values[key]; ok {
		// Drop the flashes and return it.
		delete(s.Values, key)
		flashes = v.([]interface{})
	}
	return flashes
}

// AddFlash adds a flash message to the session.
//
// A single variadic argument is accepted, and it is optional: it defines
// the flash key. If not defined "_flash" is used by default.
func (s *Session) AddFlash(value interface{}, vars ...string) {
	key := flashesKey
	if len(vars) > 0 {
		key = vars[0]
	}
	var flashes []interface{}
	if v, ok := s.Values[key]; ok {
		flashes = v.([]interface{})
	}
	s.Values[key] = append(flashes, value)
}

// Save is a convenience method to save this session. It is the same as calling
// store.Save(request, response, session)
func (s *Session) Save(c *floki.Context) error {
	return s.store.Save(c, s)
}

// Name returns the name used to register the session.
func (s *Session) Name() string {
	return s.name
}

// Store returns the session store used to register the session.
func (s *Session) Store() Store {
	return s.store
}

func (s *Session) Get(key interface{}) interface{} {
	return s.Values[key]
}

func (s *Session) Set(key interface{}, val interface{}) {
	s.Values[key] = val
	s.dirty = true
}

func (s *Session) Delete(key interface{}) {
	delete(s.Values, key)
	s.dirty = true
}

// Registry -------------------------------------------------------------------

// sessionInfo stores a session tracked by the registry.
type sessionInfo struct {
	s *Session
	e error
}

// contextKey is the type used to store the registry in the context.
//type contextKey string

// registryKey is the key used to store the registry in the context.
const registryKey string = "_sessionReg"

// GetRegistry returns a registry instance for the current request.
func GetRegistry(c *floki.Context) *Registry {
	registry, _ := c.Get(registryKey)
	if registry != nil {
		return registry.(*Registry)
	}
	newRegistry := &Registry{
		context:  c,
		sessions: make(map[string]sessionInfo),
	}
	c.Set(registryKey, newRegistry)
	return newRegistry
}

// Registry stores sessions used during a request.
type Registry struct {
	context  *floki.Context
	sessions map[string]sessionInfo
}

// Get registers and returns a session for the given name and session store.
//
// It returns a new session if there are no sessions registered for the name.
func (s *Registry) Get(store Store, name string) (session *Session, err error) {
	if info, ok := s.sessions[name]; ok {
		session, err = info.s, info.e
	} else {
		session, err = store.New(s.context, name)
		session.name = name
		s.sessions[name] = sessionInfo{s: session, e: err}
	}
	session.store = store
	return
}

// Save saves all sessions registered for the current request.
func (s *Registry) Save(c *floki.Context) error {
	var errMulti MultiError
	for name, info := range s.sessions {
		session := info.s
		if session.store == nil {
			errMulti = append(errMulti, fmt.Errorf(
				"sessions: missing store for session %q", name))
		} else if err := session.store.Save(c, session); err != nil {
			errMulti = append(errMulti, fmt.Errorf(
				"sessions: error saving session %q -- %v", name, err))
		}
	}
	if errMulti != nil {
		return errMulti
	}
	return nil
}

// Helpers --------------------------------------------------------------------

func init() {
	gob.Register([]interface{}{})
	gob.Register(floki.Model{})
}

// Save saves all sessions used during the current request.
func Save(c *floki.Context) error {
	return GetRegistry(c).Save(c)
}

// NewCookie returns an http.Cookie with the options set. It also sets
// the Expires field calculated based on the MaxAge value, for Internet
// Explorer compatibility.
func NewCookie(name, value string, options *Options) *http.Cookie {
	if options == nil {
		panic("NewCookie got <nil> options")
	}

	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     options.Path,
		Domain:   options.Domain,
		MaxAge:   options.MaxAge,
		Secure:   options.Secure,
		HttpOnly: options.HttpOnly,
	}

	if options.MaxAge > 0 {
		d := time.Duration(options.MaxAge) * time.Second
		cookie.Expires = time.Now().Add(d)
	} else if options.MaxAge < 0 {
		// Set it to the past to expire now.
		cookie.Expires = time.Unix(1, 0)
	}
	return cookie
}

// Error ----------------------------------------------------------------------

// MultiError stores multiple errors.
//
// Borrowed from the App Engine SDK.
type MultiError []error

func (m MultiError) Error() string {
	s, n := "", 0
	for _, e := range m {
		if e != nil {
			if n == 0 {
				s = e.Error()
			}
			n++
		}
	}
	switch n {
	case 0:
		return "(0 errors)"
	case 1:
		return s
	case 2:
		return s + " (and 1 other error)"
	}
	return fmt.Sprintf("%s (and %d other errors)", s, n-1)
}

/*

package sessions

import (
	"github.com/frogprog/floki"
	"github.com/gorilla/context"
	"github.com/gorilla/sessions"
	"log"
	"net/http"
)

const (
	errorFormat = "[sessions] ERROR! %s\n"
)

// Store is an interface for custom session stores.
type Store interface {
	sessions.Store
}

// Options stores configuration for a session or session store.
//
// Fields are a subset of http.Cookie fields.
type Options struct {
	Path   string
	Domain string
	// MaxAge=0 means no 'Max-Age' attribute specified.
	// MaxAge<0 means delete cookie now, equivalently 'Max-Age: 0'.
	// MaxAge>0 means Max-Age attribute present and given in seconds.
	MaxAge   int
	Secure   bool
	HttpOnly bool
}

// Session stores the values and optional configuration for a session.
type Session interface {
	// Get returns the session value associated to the given key.
	Get(key interface{}) interface{}
	// Set sets the session value associated to the given key.
	Set(key interface{}, val interface{})
	// Delete removes the session value associated to the given key.
	Delete(key interface{})
	// Clear deletes all values in the session.
	Clear()
	// AddFlash adds a flash message to the session.
	// A single variadic argument is accepted, and it is optional: it defines the flash key.
	// If not defined "_flash" is used by default.
	AddFlash(value interface{}, vars ...string)
	// Flashes returns a slice of flash messages from the session.
	// A single variadic argument is accepted, and it is optional: it defines the flash key.
	// If not defined "_flash" is used by default.
	Flashes(vars ...string) []interface{}
	// Options sets confuguration for a session.
	Options(Options)
	Values() map[interface{}]interface{}
}

func flushSession(c *floki.Context) {
	s := c.MustGet("_session").(*session)
	c.Logger().Println("flushing session..")
	if s.Written() {
		c.Logger().Println("flushed:", s.Session().Values)
		check(s.Session().Save(c.Request, c.Writer), c.Logger())
	}
}

// Sessions is a Middleware that maps a session.Session service into the Floki handler chain.
// Sessions can use a number of storage solutions with the given store.
func Sessions(name string, store Store) floki.HandlerFunc {
	return func(c *floki.Context) {
		r := c.Request
		l := c.Logger()

		// Map to the Session interface
		s := &session{name, r, l, store, nil, false}
		c.Set("_session", s)

		// export session values to the request context
		c.Set("session", s.Session().Values)

		c.BeforeDestroy(flushSession)

		// clear the context, we don't need to use
		// gorilla context and we don't want memory leaks
		defer context.Clear(r)

		c.Next()

	}
}

type session struct {
	name    string
	request *http.Request
	logger  *log.Logger
	store   Store
	session *sessions.Session
	written bool
}

func (s *session) Get(key interface{}) interface{} {
	return s.Session().Values[key]
}

func (s *session) Set(key interface{}, val interface{}) {
	s.Session().Values[key] = val
	s.written = true
}

func (s *session) Delete(key interface{}) {
	delete(s.Session().Values, key)
	s.written = true
}

func (s *session) Values() map[interface{}]interface{} {
	return s.Session().Values
}

func (s *session) Clear() {
	for key := range s.Session().Values {
		s.Delete(key)
	}
}

func (s *session) AddFlash(value interface{}, vars ...string) {
	s.Session().AddFlash(value, vars...)
	s.written = true
}

func (s *session) Flashes(vars ...string) []interface{} {
	s.written = true
	return s.Session().Flashes(vars...)
}

func (s *session) Options(options Options) {
	s.Session().Options = &sessions.Options{
		Path:     options.Path,
		Domain:   options.Domain,
		MaxAge:   options.MaxAge,
		Secure:   options.Secure,
		HttpOnly: options.HttpOnly,
	}
}

func (s *session) Session() *sessions.Session {
	if s.session == nil {
		var err error
		s.session, err = s.store.Get(s.request, s.name)
		check(err, s.logger)
	}

	return s.session
}

func (s *session) Written() bool {
	return s.written
}

func check(err error, l *log.Logger) {
	if err != nil {
		l.Printf(errorFormat, err)
	}
}

func Get(c *floki.Context) Session {
	var s *session
	s = c.MustGet("_session").(*session)
	return Session(s)
}
*/
