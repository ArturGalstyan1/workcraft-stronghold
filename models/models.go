package models

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type WebSocketMessage struct {
	Type    string                  `json:"type"`
	Message *map[string]interface{} `json:"message,omitempty"`
}

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Claims struct {
	jwt.RegisteredClaims
	APIKey string `json:"api_key"`
}

type TaskAcknowledgement struct {
	PeonID string `json:"peon_id"`
}

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "PENDING"
	TaskStatusRunning   TaskStatus = "RUNNING"
	TaskStatusSuccess   TaskStatus = "SUCCESS"
	TaskStatusFailure   TaskStatus = "FAILURE"
	TaskStatusInvalid   TaskStatus = "INVALID"
	TaskStatusCancelled TaskStatus = "CANCELLED"
)

type TaskPayload struct {
	TaskArgs             []interface{}          `json:"task_args"`
	TaskKwargs           map[string]interface{} `json:"task_kwargs"`
	PrerunHandlerArgs    []interface{}          `json:"prerun_handler_args"`
	PrerunHandlerKwargs  map[string]interface{} `json:"prerun_handler_kwargs"`
	PostrunHandlerArgs   []interface{}          `json:"postrun_handler_args"`
	PostrunHandlerKwargs map[string]interface{} `json:"postrun_handler_kwargs"`
}

type Task struct {
	ID             string      `json:"id"`
	TaskName       string      `json:"task_name"`
	Status         TaskStatus  `json:"status"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	PeonId         *string     `json:"peon_id"`
	Queue          string      `json:"queue"`
	Payload        TaskPayload `json:"payload"`
	Result         interface{} `json:"result"`
	RetryOnFailure bool        `json:"retry_on_failure"`
	RetryCount     int         `json:"retry_count"`
	RetryLimit     int         `json:"retry_limit"`
}

type Peon struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	LastHeartbeat string  `json:"last_heartbeat"`
	CurrentTask   *string `json:"current_task"`
	Queues        *string `json:"queues"`
}

type FilterOperator string

const (
	FilterOpEquals    FilterOperator = "eq"
	FilterOpGreater   FilterOperator = "gt"
	FilterOpLess      FilterOperator = "lt"
	FilterOpGreaterEq FilterOperator = "gte"
	FilterOpLessEq    FilterOperator = "lte"
	FilterOpIn        FilterOperator = "in"
	FilterOpNotIn     FilterOperator = "not_in"
)

type FilterCondition struct {
	Op    FilterOperator `json:"op"`
	Value interface{}    `json:"value"`
}

type QueryParams struct {
	Page    int         `json:"page"`
	PerPage int         `json:"per_page"`
	Order   *OrderParam `json:"order,omitempty"`
	Filter  any         `json:"filter,omitempty"` // will be TaskFilter or PeonFilter
}

type OrderParam struct {
	Field string `json:"field"` // e.g., "created_at", "last_heartbeat"
	Dir   string `json:"dir"`   // "ASC" or "DESC"
}

// For Tasks
type TaskQuery struct {
	QueryParams
	Filter *TaskFilter `json:"filter,omitempty"`
}

// For Peons
type PeonQuery struct {
	QueryParams
	Filter *PeonFilter `json:"filter,omitempty"`
}

type TaskFilter struct {
	Status    *FilterCondition `json:"status,omitempty"`
	CreatedAt *FilterCondition `json:"created_at,omitempty"`
	TaskName  *FilterCondition `json:"task_name,omitempty"`
	Queue     *FilterCondition `json:"queue,omitempty"`
	PeonId    *FilterCondition `json:"peon_id,omitempty"`
}

type PeonFilter struct {
	Status        *FilterCondition `json:"status,omitempty"`
	LastHeartbeat *FilterCondition `json:"last_heartbeat,omitempty"`
	CurrentTask   *FilterCondition `json:"current_task,omitempty"`
	Queues        *FilterCondition `json:"queues,omitempty"`
}

type PaginatedResponse struct {
	Page       int         `json:"page"`
	PerPage    int         `json:"per_page"`
	TotalItems int         `json:"total_items"`
	TotalPages int         `json:"total_pages"`
	Items      interface{} `json:"items"`
}
