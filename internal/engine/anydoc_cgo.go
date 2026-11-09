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
	"unsafe"
)

// HasInProcessAnyDoc reports true because this binary was built with in-process anydoc support.
const HasInProcessAnyDoc = true

func convertAnyDoc(ctx context.Context, filePath string) (string, error) {
	if _, err := os.Stat(filePath); err != nil {
		return "", err
	}

	cPath := C.CString(filePath)
	defer C.free(unsafe.Pointer(cPath))

	res := C.anydoc_convert(cPath)
	if res == nil {
		return "", fmt.Errorf("anydoc conversion failed for %s", filePath)
	}
	defer C.anydoc_free(res)

	return C.GoString(res), nil
}
