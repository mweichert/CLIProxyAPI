package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

// trackInFlight wraps the HTTP handler so shutdown can report how many requests
// it is draining. It does not gate requests: once http.Server.Shutdown begins,
// net/http itself refuses new work (listeners close, idle keep-alive connections
// close, and a request read after shutdown began is dropped unanswered).
func (s *Server) trackInFlight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.inFlight.Add(1)
		defer s.inFlight.Add(-1)
		next.ServeHTTP(w, r)
	})
}

// drainHTTPServer lets in-flight requests finish until ctx ends, then
// force-closes whatever is still open so the drain window is a real bound.
func (s *Server) drainHTTPServer(ctx context.Context) error {
	inFlight := s.inFlight.Load()
	if inFlight > 0 {
		if deadline, ok := ctx.Deadline(); ok {
			log.Infof("shutdown: draining %d in-flight request(s) for up to %s", inFlight, time.Until(deadline).Round(100*time.Millisecond))
		} else {
			log.Infof("shutdown: draining %d in-flight request(s)", inFlight)
		}
	}
	started := time.Now()
	errShutdown := s.server.Shutdown(ctx)
	if errShutdown == nil {
		if inFlight > 0 {
			log.Infof("shutdown: in-flight requests drained in %s", time.Since(started).Round(time.Millisecond))
		}
		return nil
	}
	if errors.Is(errShutdown, context.DeadlineExceeded) || errors.Is(errShutdown, context.Canceled) {
		log.Warnf("shutdown: drain window ended after %s with %d request(s) still in flight; closing their connections", time.Since(started).Round(time.Millisecond), s.inFlight.Load())
		if errClose := s.server.Close(); errClose != nil {
			log.Debugf("shutdown: force-close returned: %v", errClose)
		}
	}
	return errShutdown
}
