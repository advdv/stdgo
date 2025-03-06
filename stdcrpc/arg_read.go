// Package stdcrpc provides re-usable utilities for connect rpc usage.
package stdcrpc

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

// ArgRead makes it more ergonomic to read from protobuf messages input that might turn out to be invalid. It
// can perform the error handling for this in one go and return well formatted Connect error for it.
type ArgRead struct{ errs []error }

// UUID parses a uuid string.
func (abi ArgRead) UUID(s string) (uid uuid.UUID, abo ArgRead) {
	uid, err := uuid.Parse(s)
	if err != nil {
		abi.errs = append(abi.errs, err)
		uid = uuid.Nil
	}

	return uid, abi
}

// UUIDp parses a string pointer into a pointer uuid.
func (abi ArgRead) UUIDp(s *string) (uidp *uuid.UUID, abo ArgRead) {
	if s == nil {
		return nil, abi
	}

	uid, abo := abi.UUID(*s)
	if uid == uuid.Nil {
		return nil, abo
	}

	return &uid, abo
}

// Error returns the joined error as an InvalidArgument connect error or nil if there were no errors.
func (abi ArgRead) Error() error {
	joined := errors.Join(abi.errs...)
	if joined == nil {
		return nil
	}

	return connect.NewError(connect.CodeInvalidArgument, joined)
}
