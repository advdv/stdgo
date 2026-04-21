package insecureaccesstools

import (
	_ "embed"
)

// TestAccessToken1 is a pre-generated access token for testing.
//
//go:embed test_access_token1.txt
var TestAccessToken1 string

// TestAccessToken2 is a pre-generated access token for testing.
//
//go:embed test_access_token2.txt
var TestAccessToken2 string

// TestAccessToken3 is a pre-generated access token for testing.
//
//go:embed test_access_token3.txt
var TestAccessToken3 string
