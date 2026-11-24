// Package privilege manages the dedicated Unix service identity.
package privilege

import (
	"errors"
	"fmt"
)

var (
	ErrNotElevated = errors.New("system account creation requires elevated privileges")
	ErrUnsupported = errors.New("service account management is unsupported on this platform")
)

// Identity identifies the dedicated service user and its matching group.
type Identity struct {
	Name         string
	UID          int
	GID          int
	Created      bool
	GroupCreated bool
}

// SelectSystemID returns the highest unoccupied ID in the platform's safe
// service-account range, skipping IDs reserved by common system services.
func SelectSystemID(uidUsed, gidUsed map[int]bool, goos string) (int, error) {
	minimum, maximum := 200, 899
	if goos == "darwin" {
		maximum = 399
	} else if goos != "linux" && goos != "freebsd" {
		return 0, ErrUnsupported
	}
	for id := maximum; id >= minimum; id-- {
		if reservedIDs[id] || uidUsed[id] || gidUsed[id] {
			continue
		}
		return id, nil
	}
	return 0, fmt.Errorf("no available UID/GID in safe range %d-%d", minimum, maximum)
}

func validSystemID(id int, goos string) bool {
	maximum := 899
	if goos == "darwin" {
		maximum = 399
	}
	return id >= 200 && id <= maximum && !reservedIDs[id]
}

var reservedIDs = map[int]bool{
	65534: true,
	999:   true, 998: true, 997: true, 996: true, 995: true,
	994: true, 993: true, 992: true, 991: true, 990: true,
	989: true, 988: true, 987: true, 986: true, 985: true,
	984: true, 983: true, 982: true, 981: true, 980: true,
	101: true, 102: true, 103: true, 104: true, 105: true,
	106: true, 107: true, 108: true, 109: true, 110: true,
	170: true, 171: true, 172: true, 173: true, 174: true,
	175: true, 176: true, 177: true, 178: true, 179: true,
}

func requireElevated(euid int) error {
	if euid != 0 {
		return ErrNotElevated
	}
	return nil
}
