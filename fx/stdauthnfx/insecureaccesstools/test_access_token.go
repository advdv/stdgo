package insecureaccesstools

import (
	_ "embed"
)

//go:embed test_access_token1.txt
var TestAccessToken1 string

//go:embed test_access_token2.txt
var TestAccessToken2 string

//go:embed test_access_token3.txt
var TestAccessToken3 string
