// routes_custom.go
package main

import "github.com/justinas/alice"

// registerCustomRoutes houses your production payment features
func (s *server) registerCustomRoutes(c alice.Chain) {
	s.router.Handle("/chat/send/cta", c.Then(s.SendCTA())).Methods("POST")
	// s.router.Handle("/chat/send/form", c.Then(s.SendForm())).Methods("POST")
	s.router.Handle("/chat/send/pix", c.Then(s.SendPix())).Methods("POST")

	// Any future endpoints you build (like /chat/send/charge) can go here:
	// s.router.Handle("/chat/send/charge", c.Then(s.SendCharge())).Methods("POST")
}
