package via

// bitset is a small fixed-size dirty tracker. We use uint64 words; the
// fixed size is set at descriptor build time.
type bitset struct {
	words []uint64
}

func newBitset(n int) bitset {
	if n == 0 {
		return bitset{}
	}
	return bitset{words: make([]uint64, (n+63)/64)}
}

func (b *bitset) set(i int) {
	if i < 0 || i >= len(b.words)*64 {
		return
	}
	b.words[i/64] |= 1 << (i % 64)
}

func (b *bitset) get(i int) bool {
	if i < 0 || i >= len(b.words)*64 {
		return false
	}
	return b.words[i/64]&(1<<(i%64)) != 0
}

func (b *bitset) clear() {
	clear(b.words)
}

func (b *bitset) any() bool {
	for _, w := range b.words {
		if w != 0 {
			return true
		}
	}
	return false
}
