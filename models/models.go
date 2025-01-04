package models

import (
	"encoding/json"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BaseModel struct {
	ID        string     `gorm:"primarykey;type:string" json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `gorm:"index" json:"deleted_at,omitempty"`
}

type Queue struct {
	BaseModel
	TaskID string `json:"task_id" gorm:"not null;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Task   Task   `gorm:"foreignKey:TaskID"`
	Queued bool   `json:"queued" gorm:"default:false"`
}

type Task struct {
	BaseModel
	TaskName       string      `json:"task_name"`
	Status         TaskStatus  `json:"status"`
	PeonID         *string     `json:"peon_id" gorm:"type:uuid"`
	Queue          string      `json:"queue"`
	PayloadStr     string      `json:"-" gorm:"column:payload;type:text"`
	ResultStr      string      `json:"-" gorm:"column:result;type:text"`
	RetryOnFailure bool        `json:"retry_on_failure"`
	RetryCount     int         `json:"retry_count"`
	RetryLimit     int         `json:"retry_limit"`
	Payload        TaskPayload `json:"payload" gorm:"-"`
	Result         interface{} `json:"result" gorm:"-"`
}

type Stats struct {
	BaseModel
	Type     string      `json:"type"`
	ValueStr string      `json:"-" gorm:"column:value;type:text"` // Hide from JSON
	PeonID   *string     `json:"peon_id" gorm:"type:uuid"`
	TaskID   *string     `json:"task_id" gorm:"type:uuid"`
	Value    interface{} `json:"value" gorm:"-"` // Use this for JSON
}

type Peon struct {
	BaseModel
	Status        string  `json:"status"`
	LastHeartbeat string  `json:"last_heartbeat"`
	CurrentTask   *string `json:"current_task" gorm:"type:uuid"`
	Queues        *string `json:"queues"`
}

type PeonUpdate struct {
	Status        *string `json:"status,omitempty" db:"status"`
	LastHeartbeat *string `json:"last_heartbeat,omitempty" db:"last_heartbeat"`
	CurrentTask   *string `json:"current_task,omitempty" db:"current_task"`
	Queues        *string `json:"queues,omitempty" db:"queues"`
}

type TaskUpdate struct {
	Status         *string      `db:"status"`
	TaskName       *string      `db:"task_name"`
	PeonID         *string      `db:"peon_id"`
	Queue          *string      `db:"queue"`
	Payload        *interface{} `db:"payload"`
	Result         *string      `db:"result"`
	RetryOnFailure *bool        `db:"retry_on_failure"`
	RetryCount     *int         `db:"retry_count"`
	RetryLimit     *int         `db:"retry_limit"`
}

type SSEMessage struct {
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
	TaskStatusPending      TaskStatus = "PENDING"
	TaskStatusRunning      TaskStatus = "RUNNING"
	TaskStatusSuccess      TaskStatus = "SUCCESS"
	TaskStatusFailure      TaskStatus = "FAILURE"
	TaskStatusInvalid      TaskStatus = "INVALID"
	TaskStatusCancelled    TaskStatus = "CANCELLED"
	TaskStatusAcknowledged TaskStatus = "ACKNOWLEDGED"
)

type TaskPayload struct {
	TaskArgs             []interface{}          `json:"task_args"`
	TaskKwargs           map[string]interface{} `json:"task_kwargs"`
	PrerunHandlerArgs    []interface{}          `json:"prerun_handler_args"`
	PrerunHandlerKwargs  map[string]interface{} `json:"prerun_handler_kwargs"`
	PostrunHandlerArgs   []interface{}          `json:"postrun_handler_args"`
	PostrunHandlerKwargs map[string]interface{} `json:"postrun_handler_kwargs"`
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
	PeonID    *FilterCondition `json:"peon_id,omitempty"`
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

func (t *Task) BeforeSave(tx *gorm.DB) error {
	if t.Payload.TaskKwargs == nil {
		t.Payload.TaskKwargs = make(map[string]interface{})
	}
	if t.Payload.PrerunHandlerArgs == nil {
		t.Payload.PrerunHandlerArgs = make([]interface{}, 0)
	}
	if t.Payload.PrerunHandlerKwargs == nil {
		t.Payload.PrerunHandlerKwargs = make(map[string]interface{})
	}
	if t.Payload.PostrunHandlerArgs == nil {
		t.Payload.PostrunHandlerArgs = make([]interface{}, 0)
	}
	if t.Payload.PostrunHandlerKwargs == nil {
		t.Payload.PostrunHandlerKwargs = make(map[string]interface{})
	}

	// Now serialize to PayloadStr
	if payload, err := json.Marshal(t.Payload); err == nil {
		t.PayloadStr = string(payload)
	} else {
		return err
	}

	if t.Result != nil {
		if result, err := json.Marshal(t.Result); err == nil {
			t.ResultStr = string(result)
		} else {
			return err
		}
	}
	return nil
}

func (t *Task) AfterFind(tx *gorm.DB) error {
	var payload TaskPayload
	if err := json.Unmarshal([]byte(t.PayloadStr), &payload); err != nil {
		return err
	}
	t.Payload = payload

	if t.ResultStr != "" {
		var result interface{}
		if err := json.Unmarshal([]byte(t.ResultStr), &result); err != nil {
			return err
		}
		t.Result = result
	}
	return nil
}

func (s *Stats) BeforeSave(tx *gorm.DB) error {
	if s.Value != nil {
		if value, err := json.Marshal(s.Value); err == nil {
			s.ValueStr = string(value)
		} else {
			return err
		}
	}
	return nil
}

func (s *Stats) AfterFind(tx *gorm.DB) error {
	if s.ValueStr != "" {
		var value interface{}
		if err := json.Unmarshal([]byte(s.ValueStr), &value); err != nil {
			return err
		}
		s.Value = value
	}
	return nil
}

func (base *BaseModel) BeforeCreate(tx *gorm.DB) error {
	if base.ID == "" {
		base.ID = uuid.New().String()
	}
	return nil
}
