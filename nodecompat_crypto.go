package ramune

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"crypto"
	"crypto/aes"
	gocipher "crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"golang.org/x/crypto/scrypt"
)

// goCryptoRandomBytes generates cryptographically secure random bytes.
// args: [length] → hex string
func goCryptoRandomBytes(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("randomBytes: length required")
	}
	n, ok := args[0].(float64)
	if !ok {
		return nil, fmt.Errorf("randomBytes: length must be number")
	}
	buf := make([]byte, int(n))
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return hex.EncodeToString(buf), nil
}

func newHashFunc(algorithm string) (hash.Hash, error) {
	switch strings.ToLower(algorithm) {
	case "sha256", "sha-256":
		return sha256.New(), nil
	case "sha1", "sha-1":
		return sha1.New(), nil
	case "sha512", "sha-512":
		return sha512.New(), nil
	case "sha384", "sha-384":
		return sha512.New384(), nil
	case "md5":
		return md5.New(), nil
	default:
		return nil, fmt.Errorf("unsupported hash algorithm: %s", algorithm)
	}
}

// goCryptoHash computes a hash digest.
// args: [algorithm, data, inputEncoding, outputEncoding] → hex string
func goCryptoHash(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("hash: algorithm and data required")
	}
	algorithm, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("hash: algorithm must be string")
	}
	data, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("hash: data must be string")
	}
	h, err := newHashFunc(algorithm)
	if err != nil {
		return nil, err
	}
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// goCryptoHmac computes an HMAC.
// args: [algorithm, data, key] → hex string
func goCryptoHmac(args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("hmac: algorithm, data, and key required")
	}
	algorithm, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("hmac: algorithm must be string")
	}
	data, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("hmac: data must be string")
	}
	key, ok := args[2].(string)
	if !ok {
		return nil, fmt.Errorf("hmac: key must be string")
	}
	newFunc, err := newHashConstructor(algorithm)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(newFunc, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func newHashConstructor(algorithm string) (func() hash.Hash, error) {
	switch strings.ToLower(algorithm) {
	case "sha256", "sha-256":
		return sha256.New, nil
	case "sha1", "sha-1":
		return sha1.New, nil
	case "sha512", "sha-512":
		return sha512.New, nil
	case "sha384", "sha-384":
		return sha512.New384, nil
	case "md5":
		return md5.New, nil
	default:
		return nil, fmt.Errorf("unsupported hash algorithm: %s", algorithm)
	}
}

func goZlibGzip(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("gzipSync: data required")
	}
	data, _ := args[0].(string)
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write([]byte(data))
	w.Close()
	return hex.EncodeToString(buf.Bytes()), nil
}

func goZlibGunzip(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("gunzipSync: data required")
	}
	data, _ := args[0].(string)
	compressed, err := hex.DecodeString(data)
	if err != nil {
		return nil, err
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return string(out), nil
}

func goZlibDeflate(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("deflateSync: data required")
	}
	data, _ := args[0].(string)
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	w.Write([]byte(data))
	w.Close()
	return hex.EncodeToString(buf.Bytes()), nil
}

func goZlibInflate(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("inflateSync: data required")
	}
	data, _ := args[0].(string)
	compressed, err := hex.DecodeString(data)
	if err != nil {
		return nil, err
	}
	r := flate.NewReader(bytes.NewReader(compressed))
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return string(out), nil
}

func goZlibBrotliCompress(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("brotliCompressSync: data required")
	}
	data, _ := args[0].(string)
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	w.Write([]byte(data))
	w.Close()
	return hex.EncodeToString(buf.Bytes()), nil
}

func goZlibBrotliDecompress(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("brotliDecompressSync: data required")
	}
	data, _ := args[0].(string)
	compressed, err := hex.DecodeString(data)
	if err != nil {
		return nil, err
	}
	r := brotli.NewReader(bytes.NewReader(compressed))
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return string(out), nil
}

func goCryptoCipher(args []any) (any, error) {
	if len(args) < 4 {
		return nil, fmt.Errorf("cipher: algorithm, key, iv, data required")
	}
	algorithm, _ := args[0].(string)
	keyHex, _ := args[1].(string)
	ivHex, _ := args[2].(string)
	data, _ := args[3].(string)

	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("cipher: invalid key hex: %w", err)
	}
	iv, err := hex.DecodeString(ivHex)
	if err != nil {
		return nil, fmt.Errorf("cipher: invalid iv hex: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	switch {
	case strings.HasSuffix(algorithm, "-cbc"):
		plaintext := pkcs7Pad([]byte(data), aes.BlockSize)
		ciphertext := make([]byte, len(plaintext))
		mode := gocipher.NewCBCEncrypter(block, iv)
		mode.CryptBlocks(ciphertext, plaintext)
		return hex.EncodeToString(ciphertext), nil
	default:
		return nil, fmt.Errorf("cipher: unsupported algorithm %s", algorithm)
	}
}

func goCryptoDecipher(args []any) (any, error) {
	if len(args) < 4 {
		return nil, fmt.Errorf("decipher: algorithm, key, iv, data required")
	}
	algorithm, _ := args[0].(string)
	keyHex, _ := args[1].(string)
	ivHex, _ := args[2].(string)
	dataHex, _ := args[3].(string)

	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("decipher: invalid key hex: %w", err)
	}
	iv, err := hex.DecodeString(ivHex)
	if err != nil {
		return nil, fmt.Errorf("decipher: invalid iv hex: %w", err)
	}
	ciphertext, err := hex.DecodeString(dataHex)
	if err != nil {
		return nil, fmt.Errorf("decipher: invalid data hex: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	switch {
	case strings.HasSuffix(algorithm, "-cbc"):
		plaintext := make([]byte, len(ciphertext))
		mode := gocipher.NewCBCDecrypter(block, iv)
		mode.CryptBlocks(plaintext, ciphertext)
		plaintext = pkcs7Unpad(plaintext)
		return string(plaintext), nil
	default:
		return nil, fmt.Errorf("decipher: unsupported algorithm %s", algorithm)
	}
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := make([]byte, padding)
	for i := range padtext {
		padtext[i] = byte(padding)
	}
	return append(data, padtext...)
}

func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	padding := int(data[len(data)-1])
	if padding > len(data) {
		return data
	}
	return data[:len(data)-padding]
}

func goCryptoRandomInt(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("randomInt: min and max required")
	}
	minVal, _ := args[0].(float64)
	maxVal, _ := args[1].(float64)
	diff := int(maxVal) - int(minVal)
	if diff <= 0 {
		return minVal, nil
	}
	buf := make([]byte, 4)
	rand.Read(buf)
	n := int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
	if n < 0 {
		n = -n
	}
	return float64(int(minVal) + n%diff), nil
}

// goCryptoScrypt implements crypto.scryptSync.
// args: [password, salt, keylen] → hex string
func goCryptoScrypt(args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("scryptSync: password, salt, and keylen required")
	}
	password, _ := args[0].(string)
	salt, _ := args[1].(string)
	keylen, ok := args[2].(float64)
	if !ok {
		return nil, fmt.Errorf("scryptSync: keylen must be number")
	}
	// Node.js defaults: N=16384, r=8, p=1
	key, err := scrypt.Key([]byte(password), []byte(salt), 16384, 8, 1, int(keylen))
	if err != nil {
		return nil, err
	}
	return hex.EncodeToString(key), nil
}

// goCryptoPbkdf2 implements crypto.pbkdf2Sync.
// args: [password, salt, iterations, keylen, digest] → hex string
func goCryptoPbkdf2(args []any) (any, error) {
	if len(args) < 5 {
		return nil, fmt.Errorf("pbkdf2Sync: password, salt, iterations, keylen, digest required")
	}
	password, _ := args[0].(string)
	salt, _ := args[1].(string)
	iterations, _ := args[2].(float64)
	keylen, _ := args[3].(float64)
	digest, _ := args[4].(string)

	hashFunc, err := newHashConstructor(digest)
	if err != nil {
		return nil, err
	}
	key, err2 := pbkdf2.Key(hashFunc, password, []byte(salt), int(iterations), int(keylen))
	if err2 != nil {
		return nil, err2
	}
	return hex.EncodeToString(key), nil
}

// goCryptoSign signs data with a PEM-encoded private key.
// args: [algorithm, data, privateKeyPEM] → hex signature
func goCryptoSign(args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("sign: algorithm, data, and privateKey required")
	}
	algorithm, _ := args[0].(string)
	data, _ := args[1].(string)
	keyPEM, _ := args[2].(string)

	hashAlgo, hashVal, err := hashData(algorithm, []byte(data))
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, fmt.Errorf("sign: failed to parse PEM block")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		rsaKey, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err2 != nil {
			ecKey, err3 := x509.ParseECPrivateKey(block.Bytes)
			if err3 != nil {
				return nil, fmt.Errorf("sign: unsupported key format: %v", err)
			}
			key = ecKey
		} else {
			key = rsaKey
		}
	}

	switch k := key.(type) {
	case *rsa.PrivateKey:
		sig, err := rsa.SignPKCS1v15(rand.Reader, k, hashAlgo, hashVal)
		if err != nil {
			return nil, err
		}
		return hex.EncodeToString(sig), nil
	case *ecdsa.PrivateKey:
		sig, err := ecdsa.SignASN1(rand.Reader, k, hashVal)
		if err != nil {
			return nil, err
		}
		return hex.EncodeToString(sig), nil
	default:
		return nil, fmt.Errorf("sign: unsupported key type %T", key)
	}
}

// goCryptoVerify verifies a signature with a PEM-encoded public key.
// args: [algorithm, data, publicKeyPEM, signatureHex] → bool
func goCryptoVerify(args []any) (any, error) {
	if len(args) < 4 {
		return nil, fmt.Errorf("verify: algorithm, data, publicKey, and signature required")
	}
	algorithm, _ := args[0].(string)
	data, _ := args[1].(string)
	keyPEM, _ := args[2].(string)
	sigHex, _ := args[3].(string)

	hashAlgo, hashVal, err := hashData(algorithm, []byte(data))
	if err != nil {
		return nil, err
	}

	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return nil, fmt.Errorf("verify: invalid signature hex: %w", err)
	}

	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, fmt.Errorf("verify: failed to parse PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	switch k := pub.(type) {
	case *rsa.PublicKey:
		err := rsa.VerifyPKCS1v15(k, hashAlgo, hashVal, sig)
		return err == nil, nil
	case *ecdsa.PublicKey:
		ok := ecdsa.VerifyASN1(k, hashVal, sig)
		return ok, nil
	default:
		return nil, fmt.Errorf("verify: unsupported key type %T", pub)
	}
}

// goCryptoGenerateKeyPair generates an RSA or EC key pair.
// args: [type, optionsJSON] → JSON {publicKey, privateKey} as PEM strings
func goCryptoGenerateKeyPair(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("generateKeyPairSync: type required")
	}
	keyType, _ := args[0].(string)

	modulusLength := 2048
	namedCurve := "P-256"
	if len(args) >= 2 {
		if opts, ok := args[1].(string); ok {
			if strings.Contains(opts, "4096") {
				modulusLength = 4096
			} else if strings.Contains(opts, "1024") {
				modulusLength = 1024
			}
			if strings.Contains(opts, "P-384") || strings.Contains(opts, "p384") || strings.Contains(opts, "secp384r1") {
				namedCurve = "P-384"
			} else if strings.Contains(opts, "P-521") || strings.Contains(opts, "p521") || strings.Contains(opts, "secp521r1") {
				namedCurve = "P-521"
			}
		}
	}

	var privKeyBytes, pubKeyBytes []byte

	switch strings.ToLower(keyType) {
	case "rsa":
		privKey, err := rsa.GenerateKey(rand.Reader, modulusLength)
		if err != nil {
			return nil, err
		}
		privKeyBytes, _ = x509.MarshalPKCS8PrivateKey(privKey)
		pubKeyBytes, _ = x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	case "ec":
		var curve elliptic.Curve
		switch namedCurve {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("generateKeyPairSync: unsupported curve %s", namedCurve)
		}
		privKey, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return nil, err
		}
		privKeyBytes, _ = x509.MarshalPKCS8PrivateKey(privKey)
		pubKeyBytes, _ = x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	default:
		return nil, fmt.Errorf("generateKeyPairSync: unsupported type %s (use 'rsa' or 'ec')", keyType)
	}

	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privKeyBytes}))
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes}))

	return `{"publicKey":` + jsonQuote(pubPEM) + `,"privateKey":` + jsonQuote(privPEM) + `}`, nil
}

func hashData(algorithm string, data []byte) (crypto.Hash, []byte, error) {
	algo := strings.ToUpper(strings.ReplaceAll(algorithm, "-", ""))
	algo = strings.TrimPrefix(algo, "RSA")
	algo = strings.TrimPrefix(algo, "EC")

	var h hash.Hash
	var hashAlgo crypto.Hash
	switch algo {
	case "SHA256":
		h = sha256.New()
		hashAlgo = crypto.SHA256
	case "SHA1":
		h = sha1.New()
		hashAlgo = crypto.SHA1
	case "SHA512":
		h = sha512.New()
		hashAlgo = crypto.SHA512
	case "SHA384":
		h = sha512.New384()
		hashAlgo = crypto.SHA384
	default:
		return 0, nil, fmt.Errorf("unsupported sign algorithm: %s", algorithm)
	}
	h.Write(data)
	return hashAlgo, h.Sum(nil), nil
}

func jsonQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return `"` + s + `"`
}
