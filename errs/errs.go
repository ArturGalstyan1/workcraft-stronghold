package errs

import (
	"errors"
	"net/http"
)

var (
	TaskNotFoundErr           = errors.New("Task not found")
	PeonNotFoundErr           = errors.New("Peon not found")
	TaskWasNotAcknowledgedErr = errors.New("Task status needs to be ACKNOWLEDGED before it can be set to RUNNING")
	PeonIDRequiredForAckErr   = errors.New("Peon ID is required to acknowledge a task")
)

func Get(err error) (int, string) {
	switch err {
	case nil:
		// Won't be used, because we always check if err != nil
		return http.StatusOK, "OK"
	case TaskNotFoundErr:
		return http.StatusNotFound, err.Error()
	case PeonNotFoundErr:
		return http.StatusNotFound, err.Error()
	case TaskWasNotAcknowledgedErr:
		return http.StatusBadRequest, err.Error()
	case PeonIDRequiredForAckErr:
		return http.StatusBadRequest, err.Error()
	default:
		return http.StatusInternalServerError, "Unexpected internal server error occurred: " + err.Error()
	}
}
