package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/model"
)

// handlers wraps coordinator for HTTP handlers
type handlers struct {
	coordinator *coord.Coordinator
}

// Workflow handlers

func (h *handlers) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var p model.RegisterWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.coordinator.RegisterWorkflow(r.Context(), &p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *handlers) listWorkflows(w http.ResponseWriter, r *http.Request) {
	enabledOnly := r.URL.Query().Get("enabled") == "true"
	workflows, err := h.coordinator.ListWorkflows(r.Context(), enabledOnly)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(workflows)
}

func (h *handlers) getWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	version := 0
	if versionStr != "" {
		var err error
		version, err = strconv.Atoi(versionStr)
		if err != nil {
			http.Error(w, "invalid version", http.StatusBadRequest)
			return
		}
	}

	wf, err := h.coordinator.GetWorkflow(r.Context(), id, version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(wf)
}

func (h *handlers) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	version := 0
	if versionStr != "" {
		var err error
		version, err = strconv.Atoi(versionStr)
		if err != nil {
			http.Error(w, "invalid version", http.StatusBadRequest)
			return
		}
	}

	var payload struct {
		Name       *string `json:"name,omitempty"`
		Enabled    *bool   `json:"enabled,omitempty"`
		WebhookURL *string `json:"webhook_url,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.coordinator.UpdateWorkflow(r.Context(), id, version, payload.Name, payload.Enabled, payload.WebhookURL); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *handlers) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	version := 0
	if versionStr != "" {
		var err error
		version, err = strconv.Atoi(versionStr)
		if err != nil {
			http.Error(w, "invalid version", http.StatusBadRequest)
			return
		}
	}
	hardDelete := r.URL.Query().Get("hard") == "true"

	if err := h.coordinator.DeleteWorkflow(r.Context(), id, version, hardDelete); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *handlers) executeWorkflow(w http.ResponseWriter, r *http.Request) {
	// Get workflow ID from path parameter
	workflowID := chi.URLParam(r, "id")
	if workflowID == "" {
		http.Error(w, "workflow ID is required in path", http.StatusBadRequest)
		return
	}

	var p model.StartWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Use workflow ID from path parameter (ignore if provided in body)
	p.WorkflowID = workflowID

	if err := h.coordinator.StartWorkflow(r.Context(), &p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Return the run_id (generated or provided) in the response
	response := map[string]string{
		"run_id": p.RunID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(response)
}

func (h *handlers) getWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	// Support both workflow_id (from path) with run_id (from query) or just run_id
	runID := r.URL.Query().Get("run_id")
	if runID == "" {
		// If no run_id, try using the path param as run_id (for backward compatibility)
		runID = chi.URLParam(r, "id")
	}
	status, err := h.coordinator.GetWorkflowInstanceStatus(r.Context(), runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(status)
}

// Execution handlers

func (h *handlers) listExecutions(w http.ResponseWriter, r *http.Request) {
	workflowID := r.URL.Query().Get("workflow_id")
	statusStr := r.URL.Query().Get("status")
	var status *model.WorkflowInstanceStatus
	if statusStr != "" {
		s := model.WorkflowInstanceStatus(statusStr)
		status = &s
	}

	limit := 100
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if li, err := strconv.Atoi(l); err == nil {
			limit = li
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if oi, err := strconv.Atoi(o); err == nil {
			offset = oi
		}
	}

	var wfID *string
	if workflowID != "" {
		wfID = &workflowID
	}

	instances, err := h.coordinator.ListWorkflowInstances(r.Context(), wfID, status, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(instances)
}

func (h *handlers) getExecution(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	instance, err := h.coordinator.GetWorkflowInstance(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(instance)
}

func (h *handlers) cancelExecution(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var payload struct {
		Reason string `json:"reason,omitempty"`
	}
	json.NewDecoder(r.Body).Decode(&payload)

	if err := h.coordinator.CancelWorkflowInstance(r.Context(), id, payload.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
