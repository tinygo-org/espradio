package cesp

// ArenaInit hands base to the C arena allocator as the backing pool.
// base must remain reachable for the lifetime of the program.
func ArenaInit(base []byte) { arenaInit(base) }

// ArenaStats returns current arena usage and total capacity in bytes.
func ArenaStats() (used, capacity uint32) { return arenaStats() }
