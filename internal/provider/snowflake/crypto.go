/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package snowflake

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
)

// GenerateKeyPair generates a new RSA key pair for Snowflake authentication
// Returns the private key in PEM format and the public key in Snowflake's required format
func GenerateKeyPair() (privateKeyPEM []byte, publicKeyPEM string, err error) {
	// Generate 2048-bit RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate private key: %w", err)
	}

	// Encode private key to PEM in PKCS8 format (required by the gosnowflake driver)
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal private key: %w", err)
	}
	privateKeyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	// Encode public key to PEM (for Snowflake)
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal public key: %w", err)
	}

	publicKeyPEM = string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}))

	// Snowflake expects public key without PEM headers
	publicKeyPEM = stripPEMHeaders(publicKeyPEM)

	return privateKeyPEM, publicKeyPEM, nil
}

// stripPEMHeaders removes PEM headers and formatting from a public key
// Snowflake requires the key in a single line without headers
func stripPEMHeaders(pemKey string) string {
	const begin = "-----BEGIN PUBLIC KEY-----"
	const end = "-----END PUBLIC KEY-----"

	// Find and remove headers
	result := pemKey
	if idx := strings.Index(result, begin); idx >= 0 {
		result = result[idx+len(begin):]
	}
	if idx := strings.Index(result, end); idx >= 0 {
		result = result[:idx]
	}

	// Remove all whitespace (newlines, spaces, carriage returns)
	result = strings.ReplaceAll(result, "\n", "")
	result = strings.ReplaceAll(result, "\r", "")
	result = strings.ReplaceAll(result, " ", "")

	return result
}

// ParsePrivateKey parses PEM-encoded private key
// Supports both PKCS1 and PKCS8 formats
func ParsePrivateKey(privateKeyPEM []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	// Try PKCS1 format first (most common)
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err == nil {
		return privateKey, nil
	}

	// Try PKCS8 format
	key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err2 != nil {
		return nil, fmt.Errorf("failed to parse private key (tried PKCS1 and PKCS8): %w", err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA private key")
	}

	return rsaKey, nil
}
