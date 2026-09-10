package otelreg

import _ "embed"

//go:embed signal_history.json
var signalHistory string

func SignalHistory() []byte { return []byte(signalHistory) }

//go:embed availability_history.json
var availabilityHistory string

func AvailabilityHistory() []byte { return []byte(availabilityHistory) }
