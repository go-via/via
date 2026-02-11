package via

import (
	"strconv"

	"github.com/go-via/via/h"
)

type SignalType interface {
	int | int8 | int16 | int32 | int64 |
		uint | uint8 | uint16 | uint32 | uint64 |
		float32 | float64 |
		string | bool
}

func Signal[T SignalType](c *Composition, initial T) *SignalHandle[T] {
	idStr := genRandID()

	c.signals = append(c.signals, signalRegistration{
		id:      idStr,
		initial: initial,
	})

	return &SignalHandle[T]{
		id:      idStr,
		initial: initial,
	}
}

type SignalHandle[T any] struct {
	id      string
	initial T
}

func (sh *SignalHandle[T]) Get(s *Session) T {
	if s == nil || s.s == nil {
		return sh.initial
	}
	if val, ok := s.s.signals[sh.id]; ok {
		// Try direct type assertion first
		if typedVal, ok := val.(T); ok {
			return typedVal
		}
		// Handle float64 conversion (from JSON unmarshaling)
		if floatVal, ok := val.(float64); ok {
			return convertFloat64ToType(floatVal, sh.initial)
		}
		// Handle string conversion (from JSON/URL encoding)
		if strVal, ok := val.(string); ok {
			return convertStringToType(strVal, sh.initial)
		}
		return val.(T) // Will panic if conversion fails
	}
	return sh.initial
}

func convertFloat64ToType[T any](f float64, initial T) T {
	var result any
	switch any(initial).(type) {
	case int:
		result = int(f)
	case int8:
		result = int8(f)
	case int16:
		result = int16(f)
	case int32:
		result = int32(f)
	case int64:
		result = int64(f)
	case uint:
		result = uint(f)
	case uint8:
		result = uint8(f)
	case uint16:
		result = uint16(f)
	case uint32:
		result = uint32(f)
	case uint64:
		result = uint64(f)
	case float32:
		result = float32(f)
	case float64:
		result = f
	case bool:
		result = f != 0
	}
	if result != nil {
		return result.(T)
	}
	return initial
}

func convertStringToType[T any](s string, initial T) T {
	var result any
	switch any(initial).(type) {
	case int:
		if v, err := strconv.Atoi(s); err == nil {
			result = v
		}
	case int8:
		if v, err := strconv.ParseInt(s, 10, 8); err == nil {
			result = int8(v)
		}
	case int16:
		if v, err := strconv.ParseInt(s, 10, 16); err == nil {
			result = int16(v)
		}
	case int32:
		if v, err := strconv.ParseInt(s, 10, 32); err == nil {
			result = int32(v)
		}
	case int64:
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			result = v
		}
	case uint:
		if v, err := strconv.ParseUint(s, 10, 0); err == nil {
			result = uint(v)
		}
	case uint8:
		if v, err := strconv.ParseUint(s, 10, 8); err == nil {
			result = uint8(v)
		}
	case uint16:
		if v, err := strconv.ParseUint(s, 10, 16); err == nil {
			result = uint16(v)
		}
	case uint32:
		if v, err := strconv.ParseUint(s, 10, 32); err == nil {
			result = uint32(v)
		}
	case uint64:
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			result = v
		}
	case float32:
		if v, err := strconv.ParseFloat(s, 32); err == nil {
			result = float32(v)
		}
	case float64:
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			result = v
		}
	case bool:
		if v, err := strconv.ParseBool(s); err == nil {
			result = v
		}
	case string:
		result = s
	}
	if result != nil {
		return result.(T)
	}
	return initial
}

func (sh *SignalHandle[T]) Set(s *Session, value T) {
	if s == nil || s.s == nil {
		return
	}
	if s.mode == sessionModeView {
		s.warn("SignalHandle.Set() called during view render; mutation ignored")
		return
	}
	s.s.signals[sh.id] = value
	s.s.changedSignals[sh.id] = value
}

func (sh *SignalHandle[T]) Bind() h.H {
	return h.Data("bind", sh.id)
}

func (sh *SignalHandle[T]) Text() h.H {
	return h.Data("text", "$"+sh.id)
}

func (sh *SignalHandle[T]) Show() h.H {
	return h.Data("show", "$"+sh.id)
}

func (sh *SignalHandle[T]) ID() string {
	return sh.id
}

// Initial returns the initial value of the signal for registration
func (sh *SignalHandle[T]) Initial() T {
	return sh.initial
}

// Format formats the initial value as a string for the data-signals attribute
func (sh *SignalHandle[T]) FormatInitial() string {
	switch v := any(sh.initial).(type) {
	case int, int8, int16, int32, int64:
		return strconv.FormatInt(int64(v.(int)), 10)
	case uint, uint8, uint16, uint32, uint64:
		return strconv.FormatUint(uint64(v.(uint)), 10)
	case float32, float64:
		return strconv.FormatFloat(v.(float64), 'f', -1, 64)
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	default:
		return ""
	}
}
