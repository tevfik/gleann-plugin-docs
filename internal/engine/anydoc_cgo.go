//go:build anydoc

package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../ext/anydoc/include -O3
#cgo linux LDFLAGS: ${SRCDIR}/../../ext/anydoc/target/release/libanydoc_ffi.a -lpthread -ldl -lm
#cgo darwin LDFLAGS: ${SRCDIR}/../../ext/anydoc/target/release/libanydoc_ffi.a -lpthread -ldl -lm
#cgo windows LDFLAGS: ${SRCDIR}/../../ext/anydoc/target/release/libanydoc_ffi.a -lws2_32 -luserenv -lbcrypt
#include "anydoc.h"
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"
)

// HasInProcessAnyDoc reports true because this binary was built with in-process anydoc support.
const HasInProcessAnyDoc = true

// anyDocAvailable is always true: anydoc is linked into this binary.
func anyDocAvailable(func(string) (string, error)) bool { return true }

func convertAnyDoc(ctx context.Context, filePath string) (string, error) {
	if _, err := os.Stat(filePath); err != nil {
		return "", fmt.Errorf("%w: cannot read %s", ErrCorruptInput, filepath.Base(filePath))
	}

	cPath := C.CString(filePath)
	defer C.free(unsafe.Pointer(cPath))

	res := C.anydoc_convert(cPath)
	if res == nil {
		// Damaged input is a client error (422); the temporary upload path
		// is an implementation detail and never goes into the message.
		return "", fmt.Errorf("%w: anydoc conversion failed for %s", ErrCorruptInput, filepath.Base(filePath))
	}
	defer C.anydoc_free(res)

	return C.GoString(res), nil
}
