package flakerelease

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type httpRequestOptions struct {
	method        string
	url           string
	token         string
	authScheme    string
	username      string
	password      string
	accept        string
	contentType   string
	body          io.Reader
	contentLength int64
}

func multipartFileBody(fieldName string, path string) (io.ReadCloser, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}

	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	go func() {
		defer file.Close()

		part, err := multipartWriter.CreateFormFile(fieldName, filepath.Base(path))
		if err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if err := multipartWriter.Close(); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		_ = writer.Close()
	}()

	return reader, multipartWriter.FormDataContentType(), nil
}

func jsonRequest(method string, authScheme string, token string, accept string, endpoint string, payload any) ([]byte, error) {
	var body io.Reader
	contentType := ""
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
		contentType = jsonAccept
	}

	return httpRequest(httpRequestOptions{
		method:      method,
		url:         endpoint,
		token:       token,
		authScheme:  authScheme,
		accept:      accept,
		contentType: contentType,
		body:        body,
	})
}

func httpRequest(options httpRequestOptions) ([]byte, error) {
	req, err := http.NewRequest(options.method, options.url, options.body)
	if err != nil {
		return nil, err
	}
	if options.username != "" || options.password != "" {
		req.SetBasicAuth(options.username, options.password)
	} else if options.token != "" {
		authScheme := options.authScheme
		if authScheme == "" {
			authScheme = tokenAuthScheme
		}
		req.Header.Set("Authorization", authScheme+" "+options.token)
	}
	if options.accept == "" {
		options.accept = jsonAccept
	}
	req.Header.Set("Accept", options.accept)
	if options.contentType != "" {
		req.Header.Set("Content-Type", options.contentType)
	}
	if options.contentLength > 0 {
		req.ContentLength = options.contentLength
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		message = redactSecrets(message, []string{options.token, options.password, req.Header.Get("Authorization")})
		if message != "" {
			return nil, fmt.Errorf("%s %s failed: %s: %s", options.method, options.url, resp.Status, message)
		}
		return nil, fmt.Errorf("%s %s failed: %s", options.method, options.url, resp.Status)
	}
	if options.method == http.MethodDelete {
		return nil, nil
	}
	return body, nil
}
