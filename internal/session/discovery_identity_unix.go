//go:build unix

package session

import (
	"fmt"
	"os"
	"reflect"
	"syscall"
)

func discoveryFileIdentity(_ string, info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	// Linux and BSD/macOS expose the change timestamp under different names.
	value := reflect.ValueOf(stat).Elem()
	for _, name := range []string{"Ctim", "Ctimespec"} {
		if changed := value.FieldByName(name); changed.IsValid() && changed.CanInterface() {
			return fmt.Sprintf("%d:%d:%v", stat.Dev, stat.Ino, changed.Interface())
		}
	}
	return ""
}
