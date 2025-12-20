package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/model"
	"gopkg.in/yaml.v3"
)

// handlers wraps coordinator for HTTP handlers
type handlers struct {
	coordinator *coord.Coordinator
}

// Workflow handlers

func (h *handlers) createWorkflow(w http.ResponseWriter, r *http.Request) {
	// Parse JSON request body
	// Expected format: { "workflow_id": "...", "name": "...", "version": 1, "spec": "yaml string", "created_by": "..." }
	var p struct {
		WorkflowID string `json:"workflow_id"`
		Name       string `json:"name"`
		Version    int    `json:"version"`
		Spec       string `json:"spec"` // YAML string
		CreatedBy  string `json:"created_by,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON: " + err.Error()})
		return
	}

	// Validate that spec is not empty
	if p.Spec == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "spec field is required and cannot be empty"})
		return
	}

	// Validate that spec is valid YAML by parsing it
	var specObj interface{}
	if err := yaml.Unmarshal([]byte(p.Spec), &specObj); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid YAML in spec field: " + err.Error()})
		return
	}

	// Convert YAML spec to JSON for storage in database
	specJSON, err := json.Marshal(specObj)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to convert spec to JSON: " + err.Error()})
		return
	}

	// Create RegisterWorkflowRequest with JSON spec (for database storage)
	req := model.RegisterWorkflowRequest{
		WorkflowID: p.WorkflowID,
		Name:       p.Name,
		Version:    p.Version,
		Spec:       specJSON,
		CreatedBy:  p.CreatedBy,
	}

	if err := h.coordinator.RegisterWorkflow(r.Context(), &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Workflow created successfully"})
}

func (h *handlers) listWorkflows(w http.ResponseWriter, r *http.Request) {
	enabledOnly := r.URL.Query().Get("enabled") == "true"
	search := r.URL.Query().Get("search")

	// Parse pagination parameters
	page := 1
	pageSize := 10
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if pageSizeStr := r.URL.Query().Get("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	result, err := h.coordinator.ListWorkflowsWithStats(r.Context(), enabledOnly, search, page, pageSize)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Convert spec from JSON to YAML for each workflow
	workflowsWithYAML := make([]map[string]interface{}, len(result.Workflows))
	for i, wf := range result.Workflows {
		var specObj interface{}
		if err := json.Unmarshal(wf.Spec, &specObj); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to parse spec for workflow " + wf.ID + ": " + err.Error()})
			return
		}

		var specYAML interface{}
		err = yaml.Unmarshal([]byte(wf.Spec), &specYAML)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to convert spec to YAML for workflow " + wf.ID + ": " + err.Error()})
			return
		}

		workflowMap := map[string]interface{}{
			"id":           wf.ID,
			"name":         wf.Name,
			"version":      wf.Version,
			"spec":         specYAML,
			"created_by":   wf.CreatedBy,
			"enabled":      wf.Enabled,
			"deployed":     wf.Deployed,
			"created_at":   wf.CreatedAt,
			"success_rate": wf.SuccessRate,
		}
		if wf.WebhookURL.Valid {
			workflowMap["webhook_url"] = wf.WebhookURL.String
		}
		if wf.DeployedAt.Valid {
			workflowMap["deployed_at"] = wf.DeployedAt.Time.Format(time.RFC3339)
		}
		workflowsWithYAML[i] = workflowMap
	}

	response := map[string]interface{}{
		"workflows":   workflowsWithYAML,
		"total":       result.Total,
		"page":        result.Page,
		"page_size":   result.PageSize,
		"total_pages": result.TotalPages,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *handlers) getWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	latest := r.URL.Query().Get("latest") == "true"

	var wf *model.WorkflowDefinitionRecord
	var err error

	if latest {
		// Get latest version (for editing)
		wf, err = h.coordinator.GetLatestWorkflow(r.Context(), id)
	} else {
		version := 0
		if versionStr != "" {
			version, err = strconv.Atoi(versionStr)
			if err != nil {
				http.Error(w, "invalid version", http.StatusBadRequest)
				return
			}
		}
		wf, err = h.coordinator.GetWorkflow(r.Context(), id, version)
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Convert spec from JSON to YAML
	var specObj interface{}
	err = yaml.Unmarshal([]byte(wf.Spec), &specObj)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to convert spec to YAML: " + err.Error()})
		return
	}

	// Create response with YAML spec
	response := map[string]interface{}{
		"id":         wf.ID,
		"name":       wf.Name,
		"version":    wf.Version,
		"spec":       specObj,
		"created_by": wf.CreatedBy,
		"enabled":    wf.Enabled,
		"deployed":   wf.Deployed,
		"created_at": wf.CreatedAt,
	}
	if wf.WebhookURL.Valid {
		response["webhook_url"] = wf.WebhookURL.String
	}
	if wf.DeployedAt.Valid {
		response["deployed_at"] = wf.DeployedAt.Time.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Workflow updated successfully"})
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Workflow deleted successfully"})
}

func (h *handlers) deployWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	if versionStr == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "version parameter is required"})
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid version"})
		return
	}

	if err := h.coordinator.DeployWorkflow(r.Context(), id, version); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Workflow deployed successfully"})
}

func (h *handlers) rollbackWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	if versionStr == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "version parameter is required"})
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid version"})
		return
	}

	if err := h.coordinator.RollbackWorkflow(r.Context(), id, version); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Workflow rolled back successfully"})
}

func (h *handlers) getPreviousDeployedVersions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	if versionStr == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "version parameter is required"})
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid version"})
		return
	}

	versions, err := h.coordinator.GetPreviousDeployedVersions(r.Context(), id, version)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"versions": versions})
}

func (h *handlers) executeWorkflow(w http.ResponseWriter, r *http.Request) {
	// Get workflow ID from path parameter
	workflowID := chi.URLParam(r, "id")
	if workflowID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "workflow ID is required in path"})
		return
	}

	var p model.StartWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	// Use workflow ID from path parameter (ignore if provided in body)
	p.WorkflowID = workflowID

	// If workflow_version is 0 or not specified, use the deployed version
	if p.WorkflowVersion == 0 {
		deployedWorkflow, err := h.coordinator.GetWorkflow(r.Context(), workflowID, 0)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "workflow not found or no deployed version"})
			return
		}
		if !deployedWorkflow.Deployed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "workflow has no deployed version"})
			return
		}
		p.WorkflowVersion = deployedWorkflow.Version
	}

	if err := h.coordinator.StartWorkflow(r.Context(), &p); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
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

	search := r.URL.Query().Get("search")

	// Parse pagination parameters
	page := 1
	pageSize := 20
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if pageSizeStr := r.URL.Query().Get("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	var wfID *string
	if workflowID != "" {
		wfID = &workflowID
	}

	result, err := h.coordinator.ListWorkflowInstances(r.Context(), wfID, status, search, page, pageSize)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *handlers) getExecution(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Check if detailed query parameter is set
	detailed := r.URL.Query().Get("detailed") == "true"

	if detailed {
		// Return comprehensive execution details
		details, err := h.coordinator.GetExecutionDetails(r.Context(), id)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(details)
	} else {
		// Return basic execution info (backward compatible)
		instance, err := h.coordinator.GetWorkflowInstance(r.Context(), id)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(instance)
	}
}

func (h *handlers) cancelExecution(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var payload struct {
		Reason string `json:"reason,omitempty"`
	}
	json.NewDecoder(r.Body).Decode(&payload)

	if err := h.coordinator.CancelWorkflowInstance(r.Context(), id, payload.Reason); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Execution cancelled successfully"})
}

func (h *handlers) retryExecution(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	if runID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "run_id is required in path"})
		return
	}

	var p model.RetryWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Use run_id from path parameter (ignore if provided in body)
	p.RunID = runID

	// Validate that node_id is provided
	if p.NodeID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "node_id is required"})
		return
	}

	if err := h.coordinator.RetryWorkflowInstance(r.Context(), &p); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"message": "Workflow retry started successfully", "run_id": runID})
}

// Template handlers

func (h *handlers) listNodeTemplates(w http.ResponseWriter, r *http.Request) {
	runtime := r.URL.Query().Get("runtime")
	category := r.URL.Query().Get("category")

	templates, err := h.coordinator.ListNodeTemplates(r.Context(), runtime, category)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(templates)
}

func (h *handlers) getNodeTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	template, err := h.coordinator.GetNodeTemplate(r.Context(), id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(template)
}

func (h *handlers) listWorkflowTemplates(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")

	templates, err := h.coordinator.ListWorkflowTemplates(r.Context(), category)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(templates)
}

func (h *handlers) getWorkflowTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	template, err := h.coordinator.GetWorkflowTemplate(r.Context(), id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(template)
}

// Configuration handlers

func (h *handlers) listConfigurations(w http.ResponseWriter, r *http.Request) {
	configType := r.URL.Query().Get("type")

	configs, err := h.coordinator.ListConfigurations(r.Context(), configType)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(configs)
}

func (h *handlers) getConfiguration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	config, err := h.coordinator.GetConfiguration(r.Context(), id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(config)
}

func (h *handlers) createConfiguration(w http.ResponseWriter, r *http.Request) {
	var req model.CreateConfigurationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if err := h.coordinator.CreateConfiguration(r.Context(), &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Configuration created successfully"})
}

func (h *handlers) updateConfiguration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req model.UpdateConfigurationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if err := h.coordinator.UpdateConfiguration(r.Context(), id, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Configuration updated successfully"})
}

func (h *handlers) deleteConfiguration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.coordinator.DeleteConfiguration(r.Context(), id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Configuration deleted successfully"})
}

func (h *handlers) getWorkflowIssueSummary(w http.ResponseWriter, r *http.Request) {
	workflowID := chi.URLParam(r, "id")
	if workflowID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "workflow ID is required"})
		return
	}

	summary, err := h.coordinator.GetWorkflowIssueSummary(r.Context(), workflowID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}
