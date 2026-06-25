package order

import "errors"

var ErrNotFound = errors.New("order not found")

var ErrDriverBusy = errors.New("driver already has an active order")

var ErrOrderAlreadyAccepted = errors.New("order already accepted by another driver")
