package proc

// PIDAlive reports whether pid is running (not a zombie) and still the process
// that started at start, so a recycled PID never counts as alive.
func PIDAlive(pid int, start uint64) bool { return pidAlive(pid, start) }
