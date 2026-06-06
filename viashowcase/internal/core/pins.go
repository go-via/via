package core

// MaxPins caps the pins retained per room.
const MaxPins = 500

type Pin struct {
	Room     string
	Lng, Lat float64
	By       string
}

type LngLat struct{ Lng, Lat float64 }

// PinSets maps room code -> pins.
type PinSets map[string][]LngLat

// Fold returns a copy of acc with ev appended, keeping at most MaxPins
// (most recent) per room.
func (Pin) Fold(acc PinSets, ev Pin) PinSets {
	out := make(PinSets, len(acc)+1)
	for room, ps := range acc {
		out[room] = append([]LngLat(nil), ps...)
	}
	out[ev.Room] = append(out[ev.Room], LngLat{ev.Lng, ev.Lat})
	if n := len(out[ev.Room]); n > MaxPins {
		out[ev.Room] = out[ev.Room][n-MaxPins:]
	}
	return out
}

func (p PinSets) For(code string) []LngLat { return p[code] }
