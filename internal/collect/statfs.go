package collect

import "syscall"

// statfs da el total y lo usado de un sistema de archivos, en bytes. Usado
// es total menos libre (Bfree, que incluye lo reservado para root), igual
// que la columna "Usados" de df más lo reservado.
func statfs(path string) (total, used uint64, err error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, 0, err
	}
	bs := uint64(s.Bsize)
	total = s.Blocks * bs
	free := s.Bfree * bs
	if free > total {
		free = total
	}
	return total, total - free, nil
}
