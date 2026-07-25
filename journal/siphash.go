package journal

// SipHash24 computes a 64-bit SipHash-2-4 hash of data keyed by a 16-byte key.
// The key is split into two 64-bit little-endian values (k0, k1).
// Reference: https://github.com/veorq/SipHash
func SipHash24(key [16]byte, data []byte) uint64 {
	k0 := leBytesToUint64(key[0:8])
	k1 := leBytesToUint64(key[8:16])

	v0 := uint64(0x736f6d6570736575)
	v1 := uint64(0x646f72616e646f6d)
	v2 := uint64(0x6c7967656e657261)
	v3 := uint64(0x7465646279746573)

	v3 ^= k1
	v2 ^= k0
	v1 ^= k1
	v0 ^= k0

	length := len(data)
	ni := 0

	end := length - (length % 8)
	for ; ni < end; ni += 8 {
		m := leBytesToUint64(data[ni : ni+8])
		v3 ^= m
		sipround2(&v0, &v1, &v2, &v3)
		sipround2(&v0, &v1, &v2, &v3)
		v0 ^= m
	}

	var b uint64 = uint64(length) << 56
	left := length & 7
	for i := 0; i < left; i++ {
		b |= uint64(data[ni+i]) << (uint(i) * 8)
	}

	v3 ^= b
	sipround2(&v0, &v1, &v2, &v3)
	sipround2(&v0, &v1, &v2, &v3)
	v0 ^= b

	v2 ^= 0xff
	sipround2(&v0, &v1, &v2, &v3)
	sipround2(&v0, &v1, &v2, &v3)
	sipround2(&v0, &v1, &v2, &v3)
	sipround2(&v0, &v1, &v2, &v3)

	return v0 ^ v1 ^ v2 ^ v3
}

func sipround2(v0, v1, v2, v3 *uint64) {
	*v0 += *v1
	*v1 = rotl64(*v1, 13)
	*v1 ^= *v0
	*v0 = rotl64(*v0, 32)
	*v2 += *v3
	*v3 = rotl64(*v3, 16)
	*v3 ^= *v2
	*v0 += *v3
	*v3 = rotl64(*v3, 21)
	*v3 ^= *v0
	*v2 += *v1
	*v1 = rotl64(*v1, 17)
	*v1 ^= *v2
	*v2 = rotl64(*v2, 32)
}

func rotl64(x uint64, b uint) uint64 {
	return (x << b) | (x >> (64 - b))
}

func leBytesToUint64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}
