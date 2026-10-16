package shape

import "hash/maphash"

// mix is splitmix64's finalizer. It's cheap, and it spreads nearby inputs
// (sequential request IDs, small buyer numbers) across the whole range.
func mix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// Mix2 combines two values into one well-spread hash.
func Mix2(a, b uint64) uint64 { return mix(a ^ mix(b)) }

// Unit maps a hash to [0, 1).
func Unit(h uint64) float64 { return float64(h>>11) / (1 << 53) }

var strSeed = maphash.MakeSeed()

// HashString hashes a string with a per-process seed. Fingerprints and
// transaction IDs only need to agree within one running exchange.
func HashString(s string) uint64 { return maphash.String(strSeed, s) }
