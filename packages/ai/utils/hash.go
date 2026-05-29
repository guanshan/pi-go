package utils

import "strings"

func ShortHash(s string) string {
	var h1 uint32 = 0xdeadbeef
	var h2 uint32 = 0x41c6ce57
	for _, r := range s {
		ch := uint32(r)
		h1 = bitsMul(h1^ch, 2654435761)
		h2 = bitsMul(h2^ch, 1597334677)
	}
	h1 = bitsMul(h1^(h1>>16), 2246822507) ^ bitsMul(h2^(h2>>13), 3266489909)
	h2 = bitsMul(h2^(h2>>16), 2246822507) ^ bitsMul(h1^(h1>>13), 3266489909)
	return strings.ToLower(strconv36(h2) + strconv36(h1))
}

func bitsMul(a, b uint32) uint32 { return uint32(uint64(a) * uint64(b)) }

func strconv36(v uint32) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = alphabet[v%36]
		v /= 36
	}
	return string(buf[i:])
}
