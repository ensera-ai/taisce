// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/notify"
)

type notificationRegisterRequest struct {
	URL string `json:"url"`
}

type notificationEndpointRequest struct {
	ID string `json:"id"`
}

// notificationEndpointListRequest takes nothing: the credential decides the project, as it does
// everywhere else on this surface.
type notificationEndpointListRequest struct{}

type notificationDeliveryListRequest struct {
	Limit int `json:"limit,omitempty"`
}

type notificationEndpointList struct {
	Endpoints []pg.Endpoint `json:"endpoints"`
}

type notificationDeliveryList struct {
	Deliveries []pg.DeliveryRecord `json:"deliveries"`
}

type notificationDisabled struct {
	Disabled bool `json:"disabled"`
}

// registerNotificationEndpoint nominates somewhere to be told that memory formed.
//
// The destination is checked here, when somebody types it, as well as at the moment of sending. A
// refusal hours later in a delivery nobody is watching is a refusal nobody reads.
func (s *Server) registerNotificationEndpoint(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request notificationRegisterRequest
	if !decode(w, r, &request) || !s.notificationsReady(w) {
		return
	}
	if err := s.notifier.Allow(strings.TrimSpace(request.URL)); err != nil {
		s.record(r, domain.AuditNotificationRegister, grant, domain.OutcomeRefused, 0)
		// The reason is the operator's policy, and the message says so without saying what is behind
		// it: a caller probing addresses must not learn which ones exist.
		writeError(w, http.StatusBadRequest, codeInvalidDestination, err.Error())
		return
	}
	out, err := s.notifications.Register(r.Context(), grant.Project, strings.TrimSpace(request.URL))
	if err != nil {
		s.notificationError(w, r, grant, domain.AuditNotificationRegister, err)
		return
	}
	s.record(r, domain.AuditNotificationRegister, grant, domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listNotificationEndpoints(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request notificationEndpointListRequest
	if !decode(w, r, &request) || !s.notificationsReady(w) {
		return
	}
	out, err := s.notifications.Endpoints(r.Context(), grant.Project)
	if err != nil {
		s.notificationError(w, r, grant, domain.AuditNotificationList, err)
		return
	}
	s.record(r, domain.AuditNotificationList, grant, domain.OutcomeAllowed, len(out))
	writeJSON(w, http.StatusOK, notificationEndpointList{Endpoints: out})
}

func (s *Server) disableNotificationEndpoint(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request notificationEndpointRequest
	if !decode(w, r, &request) || !s.notificationsReady(w) {
		return
	}
	if err := s.notifications.Disable(r.Context(), grant.Project, request.ID); err != nil {
		s.notificationError(w, r, grant, domain.AuditNotificationDisable, err)
		return
	}
	s.record(r, domain.AuditNotificationDisable, grant, domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusOK, notificationDisabled{Disabled: true})
}

// listNotificationDeliveries is how a parked delivery is found by asking rather than by grepping.
func (s *Server) listNotificationDeliveries(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request notificationDeliveryListRequest
	if !decode(w, r, &request) || !s.notificationsReady(w) {
		return
	}
	out, err := s.notifications.Deliveries(r.Context(), grant.Project, request.Limit)
	if err != nil {
		s.notificationError(w, r, grant, domain.AuditNotificationDeliveries, err)
		return
	}
	s.record(r, domain.AuditNotificationDeliveries, grant, domain.OutcomeAllowed, len(out))
	writeJSON(w, http.StatusOK, notificationDeliveryList{Deliveries: out})
}

// notificationsReady refuses every route when the operator has named no destinations.
//
// Refused rather than served-and-empty, because an application that registered an endpoint and got a
// receipt would reasonably believe it would be told, and a deployment that intends to tell nobody
// should say so at the first request rather than by never calling.
func (s *Server) notificationsReady(w http.ResponseWriter) bool {
	if s.notifications == nil || s.notifier == nil || !s.notifier.Enabled() {
		writeError(w, http.StatusNotImplemented, codeNotificationsOff,
			"this deployment sends no notifications: an operator has named no destination it may send to")
		return false
	}
	return true
}

func (s *Server) notificationError(w http.ResponseWriter, r *http.Request, grant credential.Grant, operation string, err error) {
	s.record(r, operation, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, pg.ErrEndpointNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "notification endpoint not found")
	case errors.Is(err, pg.ErrEndpointExists):
		writeError(w, http.StatusConflict, codeInvalidDestination, "that destination is already registered for this project")
	case errors.Is(err, notify.ErrNotPermitted):
		writeError(w, http.StatusBadRequest, codeInvalidDestination, err.Error())
	default:
		s.failed(w, err, operation, grant.Project, "the notification operation did not complete")
	}
}
