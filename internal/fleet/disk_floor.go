package fleet

import (
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

// The disk floor for mail (17 Nov). There is no limit on how much agents write to each other;
// what remains is the machine: when the volume that holds tmx's data is nearly full, new mail
// that is not the owner's is refused with a plain sentence, so a runaway cannot fill the disk
// and stop the daemon's own database. The owner's messages are always stored, and Companion's,
// which acts for the owner.
//
// The floor is 2 GB free, or a tenth of a smaller volume (the rule of antechamber.ReserveFor
// and of Python tmx's disk reserve). The configured resources.disk_reserve is not read here:
// it belongs to the resource manager, which brakes agents; this check only refuses to store.
const (
	mailDiskFloor = 2 << 30
	diskFloorTTL  = 5 * time.Second // one reading serves the mail of this long
)

// mailDiskSpace reports (free, total) bytes of the volume holding a path; tests replace it.
var mailDiskSpace = sysinfo.DiskSpace

// diskFloorLocked refuses m when the data volume is below the floor and m is not the owner's.
// A reading that fails refuses nothing.
func (f *Fleet) diskFloorLocked(m model.Message) error {
	if m.From == model.Owner || m.From == model.CompanionParty { // Companion acts for the owner
		return nil
	}
	if time.Since(f.diskReadAt) > diskFloorTTL {
		f.diskReadAt = time.Now()
		free, total, err := mailDiskSpace(f.opts.Paths.Home)
		f.diskLow, f.diskFree = false, 0
		if err == nil && total > 0 {
			floor := min(uint64(mailDiskFloor), total/10)
			f.diskLow, f.diskFree = free < floor, free
		}
	}
	if !f.diskLow {
		return nil
	}
	return errf(KindTooMany, "the disk that holds tmx's data is nearly full (%d MB free): no new mail is stored until there is room; tell your lead, do not retry in a loop",
		f.diskFree>>20)
}
