package order

import "errors"

var ErrNotFound = errors.New("order not found")

var ErrDriverBusy = errors.New("driver already has an active order")
