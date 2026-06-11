package secret

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func protect(data []byte) ([]byte, error) {
	in := blobFromBytes(data)
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	return bytesFromBlob(out), nil
}

func unprotect(data []byte) ([]byte, error) {
	in := blobFromBytes(data)
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	return bytesFromBlob(out), nil
}

func blobFromBytes(data []byte) windows.DataBlob {
	if len(data) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
}

func bytesFromBlob(blob windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
	if blob.Size == 0 || blob.Data == nil {
		return nil
	}
	view := unsafe.Slice(blob.Data, int(blob.Size))
	out := make([]byte, len(view))
	copy(out, view)
	return out
}
