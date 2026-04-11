package ramune

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

func goBunCSRFGenerate(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("CSRF.generate: secret required")
	}
	secret, _ := args[0].(string)
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(nonce)
	sig := mac.Sum(nil)
	token := base64.RawURLEncoding.EncodeToString(nonce) + "." + base64.RawURLEncoding.EncodeToString(sig)
	return token, nil
}

func goBunCSRFVerify(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("CSRF.verify: secret and token required")
	}
	secret, _ := args[0].(string)
	token, _ := args[1].(string)
	var noncePart, sigPart string
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			noncePart = token[:i]
			sigPart = token[i+1:]
			break
		}
	}
	if noncePart == "" || sigPart == "" {
		return false, nil
	}
	nonce, err := base64.RawURLEncoding.DecodeString(noncePart)
	if err != nil {
		return false, nil
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return false, nil
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(nonce)
	expected := mac.Sum(nil)
	return subtle.ConstantTimeCompare(sig, expected) == 1, nil
}

func (r *Runtime) installCSRF() error {
	if err := r.registerFuncLocked("__go_bun_csrf_generate", goBunCSRFGenerate); err != nil {
		return err
	}
	return r.registerFuncLocked("__go_bun_csrf_verify", goBunCSRFVerify)
}
