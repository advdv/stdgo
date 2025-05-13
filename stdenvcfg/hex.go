package stdenvcfg

import "encoding/hex"

// HexBytes can be used for configuration to read binary encoded values from the environment variables.
type HexBytes []byte

func (p *HexBytes) UnmarshalText(text []byte) error {
	out, err := hex.DecodeString(string(text))
	if err != nil {
		return err
	}
	*p = out
	return nil
}
