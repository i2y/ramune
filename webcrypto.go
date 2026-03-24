package ramune

import (
	gocrypto "crypto"
	"crypto/aes"
	gocipher "crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math/big"
	"strings"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/pbkdf2"
)

// installWebCrypto sets up globalThis.crypto.subtle.
// Must be called with rt.mu held, after installNodeCompat.
func (r *Runtime) installWebCrypto() error {
	if err := r.registerFuncLocked("__go_subtle_digest", goSubtleDigest); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_subtle_generate_key", goSubtleGenerateKey); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_subtle_sign", goSubtleSign); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_subtle_verify", goSubtleVerify); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_subtle_encrypt", goSubtleEncrypt); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_subtle_decrypt", goSubtleDecrypt); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_subtle_import_key", goSubtleImportKey); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_subtle_export_key", goSubtleExportKey); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_subtle_derive_bits", goSubtleDeriveBits); err != nil {
		return err
	}
	return r.execLocked(webCryptoJSSource())
}

// --- Go callbacks ---

// goSubtleDigest: args [algorithm, dataHex] → hex
func goSubtleDigest(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("digest: algorithm and data required")
	}
	algo, _ := args[0].(string)
	dataHex, _ := args[1].(string)
	data, err := hex.DecodeString(dataHex)
	if err != nil {
		return nil, err
	}
	h, err := subtleHashFunc(algo)
	if err != nil {
		return nil, err
	}
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// goSubtleGenerateKey: args [algorithmJSON] → JSON key material
func goSubtleGenerateKey(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("generateKey: algorithm required")
	}
	var alg struct {
		Name       string `json:"name"`
		Hash       string `json:"hash"`
		Length     int    `json:"length"`
		ModulusLen int    `json:"modulusLength"`
		NamedCurve string `json:"namedCurve"`
		PublicExp  int    `json:"publicExponent"`
	}
	json.Unmarshal([]byte(args[0].(string)), &alg)

	switch strings.ToUpper(alg.Name) {
	case "AES-GCM", "AES-CBC", "AES-CTR":
		length := alg.Length
		if length == 0 {
			length = 256
		}
		key := make([]byte, length/8)
		rand.Read(key)
		return `{"type":"secret","raw":"` + hex.EncodeToString(key) + `","algorithm":"` + alg.Name + `"}`, nil

	case "HMAC":
		hashName := alg.Hash
		if hashName == "" {
			hashName = "SHA-256"
		}
		length := alg.Length
		if length == 0 {
			switch strings.ToUpper(hashName) {
			case "SHA-1":
				length = 160
			case "SHA-256":
				length = 256
			case "SHA-384":
				length = 384
			case "SHA-512":
				length = 512
			default:
				length = 256
			}
		}
		key := make([]byte, length/8)
		rand.Read(key)
		return `{"type":"secret","raw":"` + hex.EncodeToString(key) + `","algorithm":"HMAC","hash":"` + hashName + `"}`, nil

	case "RSASSA-PKCS1-V1_5", "RSA-PSS", "RSA-OAEP":
		bits := alg.ModulusLen
		if bits == 0 {
			bits = 2048
		}
		privKey, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			return nil, err
		}
		privDER, _ := x509.MarshalPKCS8PrivateKey(privKey)
		pubDER, _ := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
		hashName := alg.Hash
		if hashName == "" {
			hashName = "SHA-256"
		}
		out := map[string]any{
			"type": "rsa", "algorithm": alg.Name, "hash": hashName,
			"privateRaw": hex.EncodeToString(privDER),
			"publicRaw":  hex.EncodeToString(pubDER),
		}
		b, _ := json.Marshal(out)
		return string(b), nil

	case "ECDSA", "ECDH":
		curve, err := getCurve(alg.NamedCurve)
		if err != nil {
			return nil, err
		}
		privKey, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return nil, err
		}
		privDER, _ := x509.MarshalPKCS8PrivateKey(privKey)
		pubDER, _ := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
		out := map[string]any{
			"type": "ec", "algorithm": alg.Name, "namedCurve": alg.NamedCurve,
			"privateRaw": hex.EncodeToString(privDER),
			"publicRaw":  hex.EncodeToString(pubDER),
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
	return nil, fmt.Errorf("generateKey: unsupported algorithm %s", alg.Name)
}

// goSubtleSign: args [algorithmJSON, keyJSON, dataHex] → hex signature
func goSubtleSign(args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("sign: algorithm, key, data required")
	}
	var alg struct {
		Name string `json:"name"`
		Hash string `json:"hash"`
	}
	json.Unmarshal([]byte(args[0].(string)), &alg)
	var keyData struct {
		Type       string `json:"type"`
		Raw        string `json:"raw"`
		PrivateRaw string `json:"privateRaw"`
		Hash       string `json:"hash"`
	}
	json.Unmarshal([]byte(args[1].(string)), &keyData)
	dataHex, _ := args[2].(string)
	data, _ := hex.DecodeString(dataHex)

	hashName := alg.Hash
	if hashName == "" {
		hashName = keyData.Hash
	}
	if hashName == "" {
		hashName = "SHA-256"
	}

	switch strings.ToUpper(alg.Name) {
	case "HMAC":
		rawKey, _ := hex.DecodeString(keyData.Raw)
		hf, err := subtleHashConstructor(hashName)
		if err != nil {
			return nil, err
		}
		mac := hmac.New(hf, rawKey)
		mac.Write(data)
		return hex.EncodeToString(mac.Sum(nil)), nil

	case "RSASSA-PKCS1-V1_5":
		privDER, _ := hex.DecodeString(keyData.PrivateRaw)
		key, err := x509.ParsePKCS8PrivateKey(privDER)
		if err != nil {
			return nil, err
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("sign: not an RSA key")
		}
		digest, ha, err := subtleHashData(hashName, data)
		if err != nil {
			return nil, err
		}
		sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, ha, digest)
		if err != nil {
			return nil, err
		}
		return hex.EncodeToString(sig), nil

	case "ECDSA":
		privDER, _ := hex.DecodeString(keyData.PrivateRaw)
		key, err := x509.ParsePKCS8PrivateKey(privDER)
		if err != nil {
			return nil, err
		}
		ecKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("sign: not an EC key")
		}
		h, _, err := subtleHashData(hashName, data)
		if err != nil {
			return nil, err
		}
		sig, err := ecdsa.SignASN1(rand.Reader, ecKey, h)
		if err != nil {
			return nil, err
		}
		return hex.EncodeToString(sig), nil
	}
	return nil, fmt.Errorf("sign: unsupported algorithm %s", alg.Name)
}

// goSubtleVerify: args [algorithmJSON, keyJSON, signatureHex, dataHex] → bool
func goSubtleVerify(args []any) (any, error) {
	if len(args) < 4 {
		return nil, fmt.Errorf("verify: algorithm, key, signature, data required")
	}
	var alg struct {
		Name string `json:"name"`
		Hash string `json:"hash"`
	}
	json.Unmarshal([]byte(args[0].(string)), &alg)
	var keyData struct {
		Type      string `json:"type"`
		Raw       string `json:"raw"`
		PublicRaw string `json:"publicRaw"`
		Hash      string `json:"hash"`
	}
	json.Unmarshal([]byte(args[1].(string)), &keyData)
	sigHex, _ := args[2].(string)
	dataHex, _ := args[3].(string)
	sig, _ := hex.DecodeString(sigHex)
	data, _ := hex.DecodeString(dataHex)

	hashName := alg.Hash
	if hashName == "" {
		hashName = keyData.Hash
	}
	if hashName == "" {
		hashName = "SHA-256"
	}

	switch strings.ToUpper(alg.Name) {
	case "HMAC":
		rawKey, _ := hex.DecodeString(keyData.Raw)
		hf, err := subtleHashConstructor(hashName)
		if err != nil {
			return nil, err
		}
		mac := hmac.New(hf, rawKey)
		mac.Write(data)
		return hmac.Equal(sig, mac.Sum(nil)), nil

	case "RSASSA-PKCS1-V1_5":
		pubDER, _ := hex.DecodeString(keyData.PublicRaw)
		pub, err := x509.ParsePKIXPublicKey(pubDER)
		if err != nil {
			return nil, err
		}
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("verify: not an RSA key")
		}
		h, ha, err := subtleHashData(hashName, data)
		if err != nil {
			return nil, err
		}
		return rsa.VerifyPKCS1v15(rsaPub, ha, h, sig) == nil, nil

	case "ECDSA":
		pubDER, _ := hex.DecodeString(keyData.PublicRaw)
		pub, err := x509.ParsePKIXPublicKey(pubDER)
		if err != nil {
			return nil, err
		}
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("verify: not an EC key")
		}
		h, _, err := subtleHashData(hashName, data)
		if err != nil {
			return nil, err
		}
		return ecdsa.VerifyASN1(ecPub, h, sig), nil
	}
	return nil, fmt.Errorf("verify: unsupported algorithm %s", alg.Name)
}

// goSubtleEncrypt: args [algorithmJSON, keyJSON, dataHex] → hex ciphertext
func goSubtleEncrypt(args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("encrypt: algorithm, key, data required")
	}
	var alg struct {
		Name    string `json:"name"`
		Iv      string `json:"iv"`
		Counter string `json:"counter"`
		Length  int    `json:"length"`
		TagLen  int    `json:"tagLength"`
	}
	json.Unmarshal([]byte(args[0].(string)), &alg)
	var keyData struct {
		Raw string `json:"raw"`
	}
	json.Unmarshal([]byte(args[1].(string)), &keyData)
	dataHex, _ := args[2].(string)
	plaintext, _ := hex.DecodeString(dataHex)
	rawKey, _ := hex.DecodeString(keyData.Raw)

	block, err := aes.NewCipher(rawKey)
	if err != nil {
		return nil, err
	}

	switch strings.ToUpper(alg.Name) {
	case "AES-GCM":
		iv, _ := hex.DecodeString(alg.Iv)
		aead, err := gocipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		ciphertext := aead.Seal(nil, iv, plaintext, nil)
		return hex.EncodeToString(ciphertext), nil

	case "AES-CBC":
		iv, _ := hex.DecodeString(alg.Iv)
		padded := pkcs7Pad(plaintext, aes.BlockSize)
		ciphertext := make([]byte, len(padded))
		gocipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
		return hex.EncodeToString(ciphertext), nil

	case "AES-CTR":
		counter, _ := hex.DecodeString(alg.Counter)
		ciphertext := make([]byte, len(plaintext))
		gocipher.NewCTR(block, counter).XORKeyStream(ciphertext, plaintext)
		return hex.EncodeToString(ciphertext), nil
	}
	return nil, fmt.Errorf("encrypt: unsupported algorithm %s", alg.Name)
}

// goSubtleDecrypt: args [algorithmJSON, keyJSON, dataHex] → hex plaintext
func goSubtleDecrypt(args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("decrypt: algorithm, key, data required")
	}
	var alg struct {
		Name    string `json:"name"`
		Iv      string `json:"iv"`
		Counter string `json:"counter"`
		Length  int    `json:"length"`
		TagLen  int    `json:"tagLength"`
	}
	json.Unmarshal([]byte(args[0].(string)), &alg)
	var keyData struct {
		Raw string `json:"raw"`
	}
	json.Unmarshal([]byte(args[1].(string)), &keyData)
	dataHex, _ := args[2].(string)
	ciphertext, _ := hex.DecodeString(dataHex)
	rawKey, _ := hex.DecodeString(keyData.Raw)

	block, err := aes.NewCipher(rawKey)
	if err != nil {
		return nil, err
	}

	switch strings.ToUpper(alg.Name) {
	case "AES-GCM":
		iv, _ := hex.DecodeString(alg.Iv)
		aead, err := gocipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		plaintext, err := aead.Open(nil, iv, ciphertext, nil)
		if err != nil {
			return nil, err
		}
		return hex.EncodeToString(plaintext), nil

	case "AES-CBC":
		iv, _ := hex.DecodeString(alg.Iv)
		plaintext := make([]byte, len(ciphertext))
		gocipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
		plaintext = pkcs7Unpad(plaintext)
		return hex.EncodeToString(plaintext), nil

	case "AES-CTR":
		counter, _ := hex.DecodeString(alg.Counter)
		plaintext := make([]byte, len(ciphertext))
		gocipher.NewCTR(block, counter).XORKeyStream(plaintext, ciphertext)
		return hex.EncodeToString(plaintext), nil
	}
	return nil, fmt.Errorf("decrypt: unsupported algorithm %s", alg.Name)
}

// goSubtleImportKey: args [format, keyDataHex, algorithmJSON] → JSON key
func goSubtleImportKey(args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("importKey: format, keyData, algorithm required")
	}
	format, _ := args[0].(string)
	keyHex, _ := args[1].(string)
	var alg struct {
		Name       string `json:"name"`
		Hash       string `json:"hash"`
		NamedCurve string `json:"namedCurve"`
	}
	json.Unmarshal([]byte(args[2].(string)), &alg)

	switch format {
	case "raw":
		out := map[string]any{
			"type": "secret", "raw": keyHex, "algorithm": alg.Name,
		}
		if alg.Hash != "" {
			out["hash"] = alg.Hash
		}
		b, _ := json.Marshal(out)
		return string(b), nil

	case "jwk":
		keyJSON, _ := hex.DecodeString(keyHex)
		var jwk map[string]any
		json.Unmarshal(keyJSON, &jwk)
		kty, _ := jwk["kty"].(string)

		switch strings.ToUpper(kty) {
		case "OCT":
			// Symmetric key
			kStr, _ := jwk["k"].(string)
			raw, _ := base64.RawURLEncoding.DecodeString(kStr)
			out := map[string]any{
				"type": "secret", "raw": hex.EncodeToString(raw), "algorithm": alg.Name,
			}
			if alg.Hash != "" {
				out["hash"] = alg.Hash
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		case "RSA":
			return importJWKRSA(jwk, alg.Name, alg.Hash)
		case "EC":
			return importJWKEC(jwk, alg.Name, alg.NamedCurve)
		}
		return nil, fmt.Errorf("importKey: unsupported JWK kty %s", kty)

	case "pkcs8":
		keyDER, _ := hex.DecodeString(keyHex)
		key, err := x509.ParsePKCS8PrivateKey(keyDER)
		if err != nil {
			return nil, err
		}
		switch key.(type) {
		case *rsa.PrivateKey:
			out := map[string]any{
				"type": "rsa", "algorithm": alg.Name, "hash": alg.Hash,
				"privateRaw": keyHex,
			}
			pubDER, _ := x509.MarshalPKIXPublicKey(&key.(*rsa.PrivateKey).PublicKey)
			out["publicRaw"] = hex.EncodeToString(pubDER)
			b, _ := json.Marshal(out)
			return string(b), nil
		case *ecdsa.PrivateKey:
			out := map[string]any{
				"type": "ec", "algorithm": alg.Name, "namedCurve": alg.NamedCurve,
				"privateRaw": keyHex,
			}
			pubDER, _ := x509.MarshalPKIXPublicKey(&key.(*ecdsa.PrivateKey).PublicKey)
			out["publicRaw"] = hex.EncodeToString(pubDER)
			b, _ := json.Marshal(out)
			return string(b), nil
		}
		return nil, fmt.Errorf("importKey: unsupported PKCS8 key type")

	case "spki":
		keyDER, _ := hex.DecodeString(keyHex)
		pub, err := x509.ParsePKIXPublicKey(keyDER)
		if err != nil {
			return nil, err
		}
		switch pub.(type) {
		case *rsa.PublicKey:
			out := map[string]any{
				"type": "rsa", "algorithm": alg.Name, "hash": alg.Hash,
				"publicRaw": keyHex,
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		case *ecdsa.PublicKey:
			out := map[string]any{
				"type": "ec", "algorithm": alg.Name, "namedCurve": alg.NamedCurve,
				"publicRaw": keyHex,
			}
			b, _ := json.Marshal(out)
			return string(b), nil
		}
		return nil, fmt.Errorf("importKey: unsupported SPKI key type")
	}
	return nil, fmt.Errorf("importKey: unsupported format %s", format)
}

// goSubtleExportKey: args [format, keyJSON] → hex
func goSubtleExportKey(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("exportKey: format and key required")
	}
	format, _ := args[0].(string)
	var keyData map[string]any
	json.Unmarshal([]byte(args[1].(string)), &keyData)

	switch format {
	case "raw":
		raw, _ := keyData["raw"].(string)
		return raw, nil
	case "pkcs8":
		raw, _ := keyData["privateRaw"].(string)
		return raw, nil
	case "spki":
		raw, _ := keyData["publicRaw"].(string)
		return raw, nil
	case "jwk":
		return exportJWK(keyData)
	}
	return nil, fmt.Errorf("exportKey: unsupported format %s", format)
}

// goSubtleDeriveBits: args [algorithmJSON, keyJSON, length] → hex
func goSubtleDeriveBits(args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("deriveBits: algorithm, key, length required")
	}
	var alg struct {
		Name       string  `json:"name"`
		Hash       string  `json:"hash"`
		Salt       string  `json:"salt"`
		Info       string  `json:"info"`
		Iterations float64 `json:"iterations"`
	}
	json.Unmarshal([]byte(args[0].(string)), &alg)
	var keyData struct {
		Raw string `json:"raw"`
	}
	json.Unmarshal([]byte(args[1].(string)), &keyData)
	length, _ := args[2].(float64)
	byteLen := int(length) / 8

	rawKey, _ := hex.DecodeString(keyData.Raw)

	switch strings.ToUpper(alg.Name) {
	case "HKDF":
		hashName := alg.Hash
		if hashName == "" {
			hashName = "SHA-256"
		}
		hf, err := subtleHashConstructor(hashName)
		if err != nil {
			return nil, err
		}
		salt, _ := hex.DecodeString(alg.Salt)
		info, _ := hex.DecodeString(alg.Info)
		reader := hkdf.New(hf, rawKey, salt, info)
		out := make([]byte, byteLen)
		if _, err := reader.Read(out); err != nil {
			return nil, err
		}
		return hex.EncodeToString(out), nil

	case "PBKDF2":
		hashName := alg.Hash
		if hashName == "" {
			hashName = "SHA-256"
		}
		hf, err := subtleHashConstructor(hashName)
		if err != nil {
			return nil, err
		}
		salt, _ := hex.DecodeString(alg.Salt)
		iterations := int(alg.Iterations)
		if iterations == 0 {
			iterations = 100000
		}
		out := pbkdf2.Key(rawKey, salt, iterations, byteLen, hf)
		return hex.EncodeToString(out), nil
	}
	return nil, fmt.Errorf("deriveBits: unsupported algorithm %s", alg.Name)
}

// --- Helpers ---

// normalizeHashAlgo converts Web Crypto hash names (e.g. "SHA-256") to the
// lowercase form used by newHashFunc/newHashConstructor in nodecompat_crypto.go.
func normalizeHashAlgo(algo string) string {
	return strings.ToLower(algo)
}

func subtleHashFunc(algo string) (hash.Hash, error) {
	return newHashFunc(normalizeHashAlgo(algo))
}

func subtleHashConstructor(algo string) (func() hash.Hash, error) {
	return newHashConstructor(normalizeHashAlgo(algo))
}

func subtleHashData(algo string, data []byte) ([]byte, gocrypto.Hash, error) {
	h, err := subtleHashFunc(algo)
	if err != nil {
		return nil, 0, err
	}
	h.Write(data)
	// Map to crypto.Hash for RSA operations.
	var ch gocrypto.Hash
	switch strings.ToUpper(strings.ReplaceAll(algo, "-", "")) {
	case "SHA1":
		ch = gocrypto.SHA1
	case "SHA256":
		ch = gocrypto.SHA256
	case "SHA384":
		ch = gocrypto.SHA384
	case "SHA512":
		ch = gocrypto.SHA512
	}
	return h.Sum(nil), ch, nil
}

func getCurve(name string) (elliptic.Curve, error) {
	switch strings.ToUpper(strings.ReplaceAll(name, "-", "")) {
	case "P256":
		return elliptic.P256(), nil
	case "P384":
		return elliptic.P384(), nil
	case "P521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported curve: %s", name)
	}
}

func importJWKRSA(jwk map[string]any, algorithm, hashName string) (string, error) {
	// Parse JWK RSA key components
	nB64, _ := jwk["n"].(string)
	eB64, _ := jwk["e"].(string)
	nBytes, _ := base64.RawURLEncoding.DecodeString(nB64)
	eBytes, _ := base64.RawURLEncoding.DecodeString(eB64)

	n := new(big.Int).SetBytes(nBytes)
	e := int(new(big.Int).SetBytes(eBytes).Int64())

	pub := &rsa.PublicKey{N: n, E: e}
	pubDER, _ := x509.MarshalPKIXPublicKey(pub)
	out := map[string]any{
		"type": "rsa", "algorithm": algorithm, "hash": hashName,
		"publicRaw": hex.EncodeToString(pubDER),
	}

	// If private key components present
	if dB64, ok := jwk["d"].(string); ok && dB64 != "" {
		dBytes, _ := base64.RawURLEncoding.DecodeString(dB64)
		pB64, _ := jwk["p"].(string)
		qB64, _ := jwk["q"].(string)
		pBytes, _ := base64.RawURLEncoding.DecodeString(pB64)
		qBytes, _ := base64.RawURLEncoding.DecodeString(qB64)

		privKey := &rsa.PrivateKey{
			PublicKey: *pub,
			D:         new(big.Int).SetBytes(dBytes),
			Primes:    []*big.Int{new(big.Int).SetBytes(pBytes), new(big.Int).SetBytes(qBytes)},
		}
		privKey.Precompute()
		privDER, _ := x509.MarshalPKCS8PrivateKey(privKey)
		out["privateRaw"] = hex.EncodeToString(privDER)
	}

	b, _ := json.Marshal(out)
	return string(b), nil
}

func importJWKEC(jwk map[string]any, algorithm, namedCurve string) (string, error) {
	crv, _ := jwk["crv"].(string)
	if namedCurve == "" {
		namedCurve = crv
	}
	curve, err := getCurve(namedCurve)
	if err != nil {
		return "", err
	}

	xB64, _ := jwk["x"].(string)
	yB64, _ := jwk["y"].(string)
	xBytes, _ := base64.RawURLEncoding.DecodeString(xB64)
	yBytes, _ := base64.RawURLEncoding.DecodeString(yB64)

	pub := &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}
	pubDER, _ := x509.MarshalPKIXPublicKey(pub)
	out := map[string]any{
		"type": "ec", "algorithm": algorithm, "namedCurve": namedCurve,
		"publicRaw": hex.EncodeToString(pubDER),
	}

	if dB64, ok := jwk["d"].(string); ok && dB64 != "" {
		dBytes, _ := base64.RawURLEncoding.DecodeString(dB64)
		privKey := &ecdsa.PrivateKey{
			PublicKey: *pub,
			D:         new(big.Int).SetBytes(dBytes),
		}
		privDER, _ := x509.MarshalPKCS8PrivateKey(privKey)
		out["privateRaw"] = hex.EncodeToString(privDER)
	}

	b, _ := json.Marshal(out)
	return string(b), nil
}

func exportJWK(keyData map[string]any) (string, error) {
	keyType, _ := keyData["type"].(string)
	switch keyType {
	case "secret":
		raw, _ := keyData["raw"].(string)
		rawBytes, _ := hex.DecodeString(raw)
		jwk := map[string]any{
			"kty": "oct",
			"k":   base64.RawURLEncoding.EncodeToString(rawBytes),
		}
		b, _ := json.Marshal(jwk)
		return hex.EncodeToString(b), nil
	}
	return "", fmt.Errorf("exportKey jwk: unsupported key type %s", keyType)
}

// --- JS polyfill ---

func webCryptoJSSource() string {
	return `
(function() {
	if (globalThis.crypto && globalThis.crypto.subtle) return;

	function toHex(buf) {
		if (typeof buf === 'string') return buf;
		var u8 = buf instanceof Uint8Array ? buf : new Uint8Array(buf instanceof ArrayBuffer ? buf : buf.buffer || buf);
		var hex = '';
		for (var i = 0; i < u8.length; i++) {
			var b = u8[i].toString(16);
			hex += b.length < 2 ? '0' + b : b;
		}
		return hex;
	}

	function fromHex(hex) {
		var arr = new Uint8Array(hex.length / 2);
		for (var i = 0; i < hex.length; i += 2) {
			arr[i/2] = parseInt(hex.substr(i, 2), 16);
		}
		return arr.buffer;
	}

	function normalizeAlgorithm(alg) {
		if (typeof alg === 'string') return { name: alg };
		var out = { name: alg.name };
		if (alg.hash) out.hash = typeof alg.hash === 'string' ? alg.hash : alg.hash.name;
		if (alg.iv) out.iv = toHex(alg.iv);
		if (alg.counter) out.counter = toHex(alg.counter);
		if (alg.length !== undefined) out.length = alg.length;
		if (alg.tagLength !== undefined) out.tagLength = alg.tagLength;
		if (alg.modulusLength) out.modulusLength = alg.modulusLength;
		if (alg.publicExponent) out.publicExponent = alg.publicExponent[0] || 65537;
		if (alg.namedCurve) out.namedCurve = alg.namedCurve;
		if (alg.salt) out.salt = toHex(alg.salt);
		if (alg.info) out.info = toHex(alg.info);
		if (alg.iterations) out.iterations = alg.iterations;
		return out;
	}

	function CryptoKey(type, extractable, usages, algorithm, _internal) {
		this.type = type;
		this.extractable = extractable;
		this.usages = usages;
		this.algorithm = algorithm;
		this._internal = _internal;
	}

	var subtle = {
		digest: function(algorithm, data) {
			var alg = normalizeAlgorithm(algorithm);
			var hex = __go_subtle_digest(alg.name, toHex(data));
			return Promise.resolve(fromHex(hex));
		},

		generateKey: function(algorithm, extractable, keyUsages) {
			var alg = normalizeAlgorithm(algorithm);
			var raw = __go_subtle_generate_key(JSON.stringify(alg));
			var keyInfo = JSON.parse(raw);

			if (keyInfo.type === 'secret') {
				return Promise.resolve(new CryptoKey('secret', extractable, keyUsages, alg, keyInfo));
			}
			// Key pair
			var pubKey = new CryptoKey('public', true, keyUsages.filter(function(u) { return u === 'verify' || u === 'encrypt'; }), alg, keyInfo);
			var privKey = new CryptoKey('private', extractable, keyUsages.filter(function(u) { return u === 'sign' || u === 'decrypt'; }), alg, keyInfo);
			return Promise.resolve({ publicKey: pubKey, privateKey: privKey });
		},

		sign: function(algorithm, key, data) {
			var alg = normalizeAlgorithm(algorithm);
			var hex = __go_subtle_sign(JSON.stringify(alg), JSON.stringify(key._internal), toHex(data));
			return Promise.resolve(fromHex(hex));
		},

		verify: function(algorithm, key, signature, data) {
			var alg = normalizeAlgorithm(algorithm);
			var result = __go_subtle_verify(JSON.stringify(alg), JSON.stringify(key._internal), toHex(signature), toHex(data));
			return Promise.resolve(result === true || result === 'true');
		},

		encrypt: function(algorithm, key, data) {
			var alg = normalizeAlgorithm(algorithm);
			var hex = __go_subtle_encrypt(JSON.stringify(alg), JSON.stringify(key._internal), toHex(data));
			return Promise.resolve(fromHex(hex));
		},

		decrypt: function(algorithm, key, data) {
			var alg = normalizeAlgorithm(algorithm);
			var hex = __go_subtle_decrypt(JSON.stringify(alg), JSON.stringify(key._internal), toHex(data));
			return Promise.resolve(fromHex(hex));
		},

		importKey: function(format, keyData, algorithm, extractable, keyUsages) {
			var alg = normalizeAlgorithm(algorithm);
			var hex;
			if (format === 'jwk') {
				hex = toHex(new TextEncoder().encode(JSON.stringify(keyData)));
			} else if (format === 'raw') {
				hex = toHex(keyData);
			} else {
				hex = toHex(keyData);
			}
			var raw = __go_subtle_import_key(format, hex, JSON.stringify(alg));
			var keyInfo = JSON.parse(raw);
			var type = keyInfo.privateRaw ? 'private' : (keyInfo.publicRaw ? 'public' : 'secret');
			return Promise.resolve(new CryptoKey(type, extractable, keyUsages, alg, keyInfo));
		},

		exportKey: function(format, key) {
			var hex = __go_subtle_export_key(format, JSON.stringify(key._internal));
			if (format === 'jwk') {
				var jsonStr = new TextDecoder().decode(new Uint8Array(fromHex(hex)));
				return Promise.resolve(JSON.parse(jsonStr));
			}
			return Promise.resolve(fromHex(hex));
		},

		deriveBits: function(algorithm, baseKey, length) {
			var alg = normalizeAlgorithm(algorithm);
			var hex = __go_subtle_derive_bits(JSON.stringify(alg), JSON.stringify(baseKey._internal), length);
			return Promise.resolve(fromHex(hex));
		},

		deriveKey: function(algorithm, baseKey, derivedKeyAlgorithm, extractable, keyUsages) {
			var dkAlg = normalizeAlgorithm(derivedKeyAlgorithm);
			var bits = dkAlg.length || 256;
			return this.deriveBits(algorithm, baseKey, bits).then(function(rawBits) {
				return subtle.importKey('raw', rawBits, derivedKeyAlgorithm, extractable, keyUsages);
			});
		}
	};

	if (!globalThis.crypto) globalThis.crypto = {};
	if (!globalThis.crypto.getRandomValues) {
		globalThis.crypto.getRandomValues = function(arr) {
			var hex = __go_crypto_random_bytes(arr.length);
			var bytes = [];
			for (var i = 0; i < hex.length; i += 2) { bytes.push(parseInt(hex.substr(i, 2), 16)); }
			for (var i = 0; i < arr.length; i++) arr[i] = bytes[i];
			return arr;
		};
	}
	if (!globalThis.crypto.randomUUID) {
		globalThis.crypto.randomUUID = function() {
			var hex = __go_crypto_random_bytes(16);
			return hex.substr(0,8) + '-' + hex.substr(8,4) + '-4' + hex.substr(13,3) + '-' +
				((parseInt(hex.substr(16,2),16) & 0x3f | 0x80).toString(16)) + hex.substr(18,2) + '-' + hex.substr(20,12);
		};
	}
	globalThis.crypto.subtle = subtle;
	globalThis.CryptoKey = CryptoKey;
})();
`
}
