package dispatch

import "errors"

var ErrNotFound = errors.New("dispatch record not found")

var ErrDriverNotDispatched = errors.New("driver is not dispatched for this order")
