package coord

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/util"
)

// ListConfigurations lists all configurations, optionally filtered by type
func (c *Coordinator) ListConfigurations(ctx context.Context, configType string) ([]model.ConfigurationResponse, error) {
	ctx, span := util.StartSpan(ctx, "configurations.list")
	defer util.EndSpan(span, nil)

	query := `SELECT id, name, description, type, config_json, created_by, created_at, updated_at 
	          FROM configurations WHERE 1=1`
	args := []interface{}{}
	argIndex := 1

	if configType != "" {
		query += ` AND type = $` + strconv.Itoa(argIndex)
		args = append(args, configType)
		argIndex++
	}

	query += ` ORDER BY name, created_at DESC`

	rows, err := c.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configurations []model.ConfigurationResponse
	for rows.Next() {
		var config model.ConfigurationResponse
		var description, createdBy sql.NullString
		var configJSONBytes []byte
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&config.ID, &config.Name, &description, &config.Type, &configJSONBytes,
			&createdBy, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}

		if description.Valid {
			config.Description = &description.String
		}
		if createdBy.Valid {
			config.CreatedBy = &createdBy.String
		}

		// Parse config_json
		if len(configJSONBytes) > 0 {
			if err := json.Unmarshal(configJSONBytes, &config.ConfigJSON); err != nil {
				return nil, err
			}
		} else {
			config.ConfigJSON = make(map[string]any)
		}

		config.CreatedAt = createdAt.Format(time.RFC3339)
		config.UpdatedAt = updatedAt.Format(time.RFC3339)

		configurations = append(configurations, config)
	}

	return configurations, rows.Err()
}

// GetConfiguration retrieves a configuration by ID
func (c *Coordinator) GetConfiguration(ctx context.Context, id string) (*model.ConfigurationResponse, error) {
	ctx, span := util.StartSpan(ctx, "configurations.get",
		util.Attribute("config.id", id),
	)
	defer util.EndSpan(span, nil)

	var config model.ConfigurationResponse
	var description, createdBy sql.NullString
	var configJSONBytes []byte
	var createdAt, updatedAt time.Time

	err := c.DB.QueryRow(ctx, `SELECT id, name, description, type, config_json, created_by, created_at, updated_at 
	                            FROM configurations WHERE id = $1`, id).Scan(
		&config.ID, &config.Name, &description, &config.Type, &configJSONBytes,
		&createdBy, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("configuration not found")
		}
		return nil, err
	}

	if description.Valid {
		config.Description = &description.String
	}
	if createdBy.Valid {
		config.CreatedBy = &createdBy.String
	}

	// Parse config_json
	if len(configJSONBytes) > 0 {
		if err := json.Unmarshal(configJSONBytes, &config.ConfigJSON); err != nil {
			return nil, err
		}
	} else {
		config.ConfigJSON = make(map[string]any)
	}

	config.CreatedAt = createdAt.Format(time.RFC3339)
	config.UpdatedAt = updatedAt.Format(time.RFC3339)

	return &config, nil
}

// CreateConfiguration creates a new configuration
func (c *Coordinator) CreateConfiguration(ctx context.Context, req *model.CreateConfigurationRequest) error {
	ctx, span := util.StartSpan(ctx, "configurations.create",
		util.Attribute("config.id", req.ID),
		util.Attribute("config.type", req.Type),
	)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("config_id", req.ID).
		Str("name", req.Name).
		Str("type", req.Type).
		Logger()

	logger.Info().Msg("creating configuration")

	// Marshal config_json
	configJSONBytes, err := json.Marshal(req.ConfigJSON)
	if err != nil {
		logger.Error().Err(err).Msg("failed to marshal config_json")
		util.RecordError(span, err)
		return err
	}

	tx, err := c.DB.Begin(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("failed to begin transaction")
		util.RecordError(span, err)
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `INSERT INTO configurations (id, name, description, type, config_json, created_by, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5::jsonb, $6, NOW(), NOW())`,
		req.ID, req.Name,
		sql.NullString{String: req.Description, Valid: req.Description != ""},
		req.Type, configJSONBytes,
		sql.NullString{String: req.CreatedBy, Valid: req.CreatedBy != ""},
	)
	if err != nil {
		logger.Error().Err(err).Msg("failed to insert configuration")
		util.RecordError(span, err)
		return err
	}

	logger.Info().Msg("configuration created successfully")
	return tx.Commit(ctx)
}

// UpdateConfiguration updates an existing configuration
func (c *Coordinator) UpdateConfiguration(ctx context.Context, id string, req *model.UpdateConfigurationRequest) error {
	ctx, span := util.StartSpan(ctx, "configurations.update",
		util.Attribute("config.id", id),
	)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().Str("config_id", id).Logger()

	logger.Info().Msg("updating configuration")

	tx, err := c.DB.Begin(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("failed to begin transaction")
		util.RecordError(span, err)
		return err
	}
	defer tx.Rollback(ctx)

	// Build update query dynamically based on what's provided
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if req.Name != nil {
		updates = append(updates, `name = $`+strconv.Itoa(argIndex))
		args = append(args, *req.Name)
		argIndex++
	}

	if req.Description != nil {
		updates = append(updates, `description = $`+strconv.Itoa(argIndex))
		args = append(args, sql.NullString{String: *req.Description, Valid: *req.Description != ""})
		argIndex++
	}

	if req.ConfigJSON != nil {
		configJSONBytes, err := json.Marshal(req.ConfigJSON)
		if err != nil {
			logger.Error().Err(err).Msg("failed to marshal config_json")
			util.RecordError(span, err)
			return err
		}
		updates = append(updates, `config_json = $`+strconv.Itoa(argIndex)+`::jsonb`)
		args = append(args, configJSONBytes)
		argIndex++
	}

	if len(updates) == 0 {
		return errors.New("no fields to update")
	}

	updates = append(updates, `updated_at = NOW()`)
	args = append(args, id)

	// Join updates manually
	updateClause := ""
	for i, update := range updates {
		if i > 0 {
			updateClause += ", "
		}
		updateClause += update
	}
	query := `UPDATE configurations SET ` + updateClause + ` WHERE id = $` + strconv.Itoa(argIndex)

	_, err = tx.Exec(ctx, query, args...)
	if err != nil {
		logger.Error().Err(err).Msg("failed to update configuration")
		util.RecordError(span, err)
		return err
	}

	logger.Info().Msg("configuration updated successfully")
	return tx.Commit(ctx)
}

// DeleteConfiguration deletes a configuration
func (c *Coordinator) DeleteConfiguration(ctx context.Context, id string) error {
	ctx, span := util.StartSpan(ctx, "configurations.delete",
		util.Attribute("config.id", id),
	)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().Str("config_id", id).Logger()

	logger.Info().Msg("deleting configuration")

	_, err := c.DB.Exec(ctx, `DELETE FROM configurations WHERE id = $1`, id)
	if err != nil {
		logger.Error().Err(err).Msg("failed to delete configuration")
		util.RecordError(span, err)
		return err
	}

	logger.Info().Msg("configuration deleted successfully")
	return nil
}
