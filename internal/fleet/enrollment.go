package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Only return known metric fields. A remote process does not control the API
// response schema or the error text shown while an operator enters credentials.
type hostMetrics struct {
	OS            string  `json:"os"`
	CPUCount      int     `json:"cpu_count"`
	CPUPercent    float64 `json:"cpu_percent"`
	RAMTotal      uint64  `json:"ram_total"`
	RAMAvailable  uint64  `json:"ram_available"`
	DiskTotal     uint64  `json:"disk_total"`
	DiskFree      uint64  `json:"disk_free"`
	UptimeSeconds float64 `json:"uptime_seconds"`
}

type enrollment struct {
	Host
	Metrics hostMetrics `json:"metrics"`
}

func (s *Service) enrollHost(w http.ResponseWriter, r *http.Request, actor string, testOnly bool) {
	var h Host
	if err := decode(w, r, &h); err != nil {
		http.Error(w, "invalid host request", http.StatusBadRequest)
		return
	}
	if err := validHost(h); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	action := r.Method + " " + r.URL.Path
	if err := s.Audit(actor, h.ID, action, "intent", 0); err != nil {
		http.Error(w, "audit unavailable", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, status, err := s.verifyEnrollment(ctx, h, actor, testOnly, r.Method == http.MethodPost)
	if auditErr := s.Audit(actor, h.ID, action, "result", status); auditErr != nil {
		http.Error(w, "audit result failed; check current host registration", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	reply(w, status, result)
}

func (s *Service) verifyEnrollment(ctx context.Context, h Host, actor string, testOnly, create bool) (enrollment, int, error) {
	var result enrollment
	s.Store.mu.Lock()
	original, existed := s.Store.data.Hosts[h.ID]
	storeErr := s.Store.failed
	s.Store.mu.Unlock()
	if storeErr != nil {
		return result, http.StatusServiceUnavailable, errors.New("vault unavailable")
	}
	if create && !testOnly && existed {
		return result, http.StatusConflict, errors.New("host ID exists; use PUT to replace it")
	}

	// Never hold the vault mutex across SSH. The UI and worker must continue
	// to use already enrolled hosts while an offline VM times out.
	raw, err := s.Runner.Run(ctx, h, map[string]any{"action": "metrics"})
	if ctx.Err() != nil {
		return result, http.StatusGatewayTimeout, errors.New("host check timed out or was cancelled")
	}
	if err != nil {
		for _, known := range []error{errSSHNetwork, errPrivateKey, errHostKeyMismatch, errSSHHandshake} {
			if errors.Is(err, known) {
				return result, http.StatusBadGateway, known
			}
		}
		return result, http.StatusBadGateway, errors.New("could not read VM metrics; check Python 3, root or sudo access and SSH session settings")
	}
	var metrics hostMetrics
	if json.Unmarshal(raw, &metrics) != nil || metrics.CPUCount < 1 || metrics.RAMTotal == 0 || metrics.DiskTotal == 0 {
		return result, http.StatusBadGateway, errors.New("VM returned incomplete CPU, memory or disk metrics")
	}
	result = enrollment{Host: publicHost(h), Metrics: metrics}

	s.Store.mu.Lock()
	defer s.Store.mu.Unlock()
	if s.Store.failed != nil {
		return result, http.StatusServiceUnavailable, errors.New("vault unavailable")
	}
	if !s.Guard(actor) {
		return result, http.StatusForbidden, errors.New("admin:fleet permission expired or was revoked during verification")
	}
	if testOnly {
		return result, http.StatusOK, nil
	}
	current, exists := s.Store.data.Hosts[h.ID]
	if exists != existed || current != original {
		return result, http.StatusConflict, errors.New("registration changed during verification; reload before saving")
	}
	if ctx.Err() != nil {
		return result, http.StatusGatewayTimeout, errors.New("host check timed out or was cancelled")
	}
	if exists && (current.Address != h.Address || current.Port != h.Port || current.Fingerprint != h.Fingerprint) && s.Store.data.hostBusy(h.ID) {
		return result, http.StatusConflict, errors.New("host has pending jobs; wait before changing its SSH endpoint or fingerprint")
	}
	s.Store.data.Hosts[h.ID] = h
	if err := s.Store.save(); err != nil {
		return result, http.StatusServiceUnavailable, errors.New("vault persistence failed; check current registration")
	}
	return result, http.StatusCreated, nil
}
