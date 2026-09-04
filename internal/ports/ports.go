package ports

import (
	"fmt"
	"net"
)

const (
	Min = 19400
	Max = 19499
)

func Available(port int) bool {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	return listener.Close() == nil
}

func Pick(preferred int) (int, error) {
	if preferred != 0 {
		if !Available(preferred) {
			return 0, fmt.Errorf("requested local port %d is already in use", preferred)
		}
		return preferred, nil
	}
	for port := Min; port <= Max; port++ {
		if Available(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port in range %d-%d", Min, Max)
}
