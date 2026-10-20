package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mohitkumar/composer/pkg/auth"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/model"
	"gopkg.in/yaml.v3"
)

// handlers wraps coordinator for HTTP handlers
type handlers struct {
	coordinator *coord.Coordinator
	jwtService  *auth.JWTService
	userService *auth.UserService
}

// Workflow handlers

func (h *handlers) createWorkflow(w http.ResponseWriter, r *http.Request) {
	// Parse JSON request body
	// Expected format: { "workflow_id": "...", "name": "...", "version": 1, "spec": "yaml string" }
	var p struct {
		WorkflowID string `json:"workflow_id"`
		Name       string `json:"name"`
		Version    int    `json:"version"`
		Spec       string `json:"spec"` // YAML string
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	// Validate that spec is not empty
	if p.Spec == "" {
		writeError(w, http.StatusBadRequest, "spec field is required and cannot be empty")
		return
	}

	// Validate that spec is valid YAML by parsing it
	var specObj interface{}
	if err := yaml.Unmarshal([]byte(p.Spec), &specObj); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid YAML in spec field: "+err.Error())
		return
	}

	// Convert YAML spec to JSON for storage in database
	specJSON, err := json.Marshal(specObj)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to convert spec to JSON: "+err.Error())
		return
	}

	// Get authenticated user ID from JWT token (set by middleware)
	userID := GetUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "user ID not found in request context")
		return
	}

	// Create RegisterWorkflowRequest with JSON spec (for database storage)
	req := model.RegisterWorkflowRequest{
		WorkflowID: p.WorkflowID,
		Name:       p.Name,
		Version:    p.Version,
		Spec:       specJSON,
		CreatedBy:  userID, // Use authenticated user ID
	}

	if err := h.coordinator.RegisterWorkflow(r.Context(), &req); err != nil {
		writeCoordError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"message": "Workflow created successfully"})
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
		writeCoordError(w, err)
		return
	}

	// Convert spec from JSON to YAML for each workflow
	workflowsWithYAML := make([]map[string]interface{}, len(result.Workflows))
	for i, wf := range result.Workflows {
		var specObj interface{}
		if err := json.Unmarshal(wf.Spec, &specObj); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to parse spec for workflow "+wf.ID+": "+err.Error())
			return
		}

		var specYAML interface{}
		err = yaml.Unmarshal([]byte(wf.Spec), &specYAML)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to convert spec to YAML for workflow "+wf.ID+": "+err.Error())
			return
		}

		workflowMap := map[string]interface{}{
			"id":             wf.ID,
			"name":           wf.Name,
			"version":        wf.Version,
			"spec":           specYAML,
			"created_by":     wf.CreatedBy,
			"enabled":        wf.Enabled,
			"deployed":       wf.Deployed,
			"created_at":     wf.CreatedAt,
			"success_rate":   wf.SuccessRate,
			"latest_version": wf.LatestVersion,
		}
		if wf.DeployedVersion > 0 {
			workflowMap["deployed_version"] = wf.DeployedVersion
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

	writeJSON(w, http.StatusOK, response)
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
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Convert spec from JSON to YAML
	var specObj interface{}
	err = yaml.Unmarshal([]byte(wf.Spec), &specObj)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to convert spec to YAML: "+err.Error())
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

	writeJSON(w, http.StatusOK, response)
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
		writeCoordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Workflow updated successfully"})
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
		writeCoordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Workflow deleted successfully"})
}

func (h *handlers) deployWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	if versionStr == "" {
		writeError(w, http.StatusBadRequest, "version parameter is required")
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}

	if err := h.coordinator.DeployWorkflow(r.Context(), id, version); err != nil {
		writeCoordError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Workflow deployed successfully"})
}

func (h *handlers) rollbackWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	if versionStr == "" {
		writeError(w, http.StatusBadRequest, "version parameter is required")
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}

	if err := h.coordinator.RollbackWorkflow(r.Context(), id, version); err != nil {
		writeCoordError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Workflow rolled back successfully"})
}

func (h *handlers) getPreviousDeployedVersions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versionStr := r.URL.Query().Get("version")
	if versionStr == "" {
		writeError(w, http.StatusBadRequest, "version parameter is required")
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}

	versions, err := h.coordinator.GetPreviousDeployedVersions(r.Context(), id, version)
	if err != nil {
		writeCoordError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"versions": versions})
}

func (h *handlers) executeWorkflow(w http.ResponseWriter, r *http.Request) {
	// Get workflow ID from path parameter
	workflowID := chi.URLParam(r, "id")
	if workflowID == "" {
		writeError(w, http.StatusBadRequest, "workflow ID is required in path")
		return
	}

	var p model.StartWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Use workflow ID from path parameter (ignore if provided in body)
	p.WorkflowID = workflowID

	// If workflow_version is 0 or not specified, use the deployed version
	if p.WorkflowVersion == 0 {
		deployedWorkflow, err := h.coordinator.GetWorkflow(r.Context(), workflowID, 0)
		if err != nil {
			writeError(w, http.StatusNotFound, "workflow not found or no deployed version")
			return
		}
		if !deployedWorkflow.Deployed {
			writeError(w, http.StatusBadRequest, "workflow has no deployed version")
			return
		}
		p.WorkflowVersion = deployedWorkflow.Version
	}

	if err := h.coordinator.StartWorkflow(r.Context(), &p); err != nil {
		writeCoordError(w, err)
		return
	}

	// Return the run_id (generated or provided) in the response
	response := map[string]string{
		"run_id": p.RunID,
	}
	writeJSON(w, http.StatusAccepted, response)
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
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
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
		writeCoordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handlers) getExecution(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Check if detailed query parameter is set
	detailed := r.URL.Query().Get("detailed") == "true"

	if detailed {
		// Return comprehensive execution details
		details, err := h.coordinator.GetExecutionDetails(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, details)
	} else {
		// Return basic execution info (backward compatible)
		instance, err := h.coordinator.GetWorkflowInstance(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, instance)
	}
}

func (h *handlers) cancelExecution(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var payload struct {
		Reason string `json:"reason,omitempty"`
	}
	json.NewDecoder(r.Body).Decode(&payload)

	if err := h.coordinator.CancelWorkflowInstance(r.Context(), id, payload.Reason); err != nil {
		writeCoordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Execution cancelled successfully"})
}

// signalExecution delivers a named event to a run's WAIT_FOR_EVENT node, or
// buffers it until the run reaches one.
func (h *handlers) signalExecution(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	var payload struct {
		Event   string          `json:"event"`
		Payload json.RawMessage `json:"payload,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	delivered, err := h.coordinator.SignalWorkflow(r.Context(), runID, payload.Event, payload.Payload)
	if err != nil {
		writeCoordError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": runID, "event": payload.Event, "delivered": delivered})
}

func (h *handlers) retryExecution(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required in path")
		return
	}

	var p model.RetryWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Use run_id from path parameter (ignore if provided in body)
	p.RunID = runID

	// Validate that node_id is provided
	if p.NodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}

	if err := h.coordinator.RetryWorkflowInstance(r.Context(), &p); err != nil {
		writeCoordError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Workflow retry started successfully", "run_id": runID})
}

// Template handlers

func (h *handlers) listNodeTemplates(w http.ResponseWriter, r *http.Request) {
	runtime := r.URL.Query().Get("runtime")
	category := r.URL.Query().Get("category")

	templates, err := h.coordinator.ListNodeTemplates(r.Context(), runtime, category)
	if err != nil {
		writeCoordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (h *handlers) getNodeTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	template, err := h.coordinator.GetNodeTemplate(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, template)
}

func (h *handlers) listWorkflowTemplates(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")

	templates, err := h.coordinator.ListWorkflowTemplates(r.Context(), category)
	if err != nil {
		writeCoordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (h *handlers) getWorkflowTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	template, err := h.coordinator.GetWorkflowTemplate(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, template)
}

// Configuration handlers

func (h *handlers) listConfigurations(w http.ResponseWriter, r *http.Request) {
	configType := r.URL.Query().Get("type")

	configs, err := h.coordinator.ListConfigurations(r.Context(), configType)
	if err != nil {
		writeCoordError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (h *handlers) getConfiguration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	config, err := h.coordinator.GetConfiguration(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (h *handlers) createConfiguration(w http.ResponseWriter, r *http.Request) {
	var req model.CreateConfigurationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Get authenticated user ID from JWT token (set by middleware)
	userID := GetUserID(r)
	if userID != "" {
		req.CreatedBy = userID // Use authenticated user ID
	}

	if err := h.coordinator.CreateConfiguration(r.Context(), &req); err != nil {
		writeCoordError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"message": "Configuration created successfully"})
}

func (h *handlers) updateConfiguration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req model.UpdateConfigurationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.coordinator.UpdateConfiguration(r.Context(), id, &req); err != nil {
		writeCoordError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Configuration updated successfully"})
}

func (h *handlers) deleteConfiguration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.coordinator.DeleteConfiguration(r.Context(), id); err != nil {
		writeCoordError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Configuration deleted successfully"})
}

// Auth handlers

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	// Authenticate user
	user, err := h.userService.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		if err == auth.ErrUserNotFound || err == auth.ErrInvalidPassword {
			writeError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "authentication failed")
		return
	}

	// Generate JWT token
	token, err := h.jwtService.GenerateToken(user.ID, user.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	response := model.LoginResponse{
		Token: token,
		User:  *user,
	}

	writeJSON(w, http.StatusOK, response)
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	// For JWT-based auth, logout is handled client-side by removing the token
	// This endpoint exists for consistency and potential future token blacklisting
	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out successfully"})
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a {"error": msg} JSON response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeCoordError maps the coordinator's sentinel errors to status codes;
// anything else is an internal error.
func writeCoordError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, coord.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, coord.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, coord.ErrConflict), errors.Is(err, coord.ErrRunActive), errors.Is(err, coord.ErrRunNotActive):
		status = http.StatusConflict
	}
	writeError(w, status, err.Error())
}
