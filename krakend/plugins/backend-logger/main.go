package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var ClientRegisterer = registerer("backend-logger")

var logger Logger

type registerer string

type Logger interface {
	Debug(v ...interface{})
	Info(v ...interface{})
	Warning(v ...interface{})
	Error(v ...interface{})
	Critical(v ...interface{})
	Fatal(v ...interface{})
}

func (registerer) RegisterLogger(value interface{}) {
	loadedLogger, ok := value.(Logger)
	if !ok {
		return
	}

	logger = loadedLogger
}

func (r registerer) RegisterClients(register func(
	name string,
	handler func(context.Context, map[string]interface{}) (http.Handler, error),
)) {
	register(string(r), r.newClient)
}

func (r registerer) newClient(_ context.Context, extra map[string]interface{}) (http.Handler, error) {
	name, ok := extra["name"].(string)
	if !ok || name != string(r) {
		return nil, errors.New("backend-logger: invalid plugin configuration")
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		logInfo(map[string]interface{}{
			"event":  "backend.request",
			"method": request.Method,
			"url":    request.URL.String(),
		})

		response, err := http.DefaultClient.Do(request)
		if err != nil {
			logInfo(map[string]interface{}{
				"duration_ms": time.Since(startedAt).Milliseconds(),
				"error":       err.Error(),
				"event":       "backend.response.error",
				"method":      request.Method,
				"url":         request.URL.String(),
			})
			http.Error(writer, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()

		logInfo(map[string]interface{}{
			"duration_ms": time.Since(startedAt).Milliseconds(),
			"event":       "backend.response",
			"headers":      response.Header,
			"method":      request.Method,
			"status":      response.StatusCode,
			"url":         request.URL.String(),
		})

		for key, values := range response.Header {
			for _, value := range values {
				writer.Header().Add(key, value)
			}
		}
		writer.WriteHeader(response.StatusCode)

		if _, err := io.Copy(writer, response.Body); err != nil {
			logInfo(map[string]interface{}{
				"error":  err.Error(),
				"event":  "backend.response.body_copy_error",
				"method": request.Method,
				"url":    request.URL.String(),
			})
		}
	}), nil
}

func logInfo(event map[string]interface{}) {
	if logger == nil {
		return
	}

	message, err := json.Marshal(event)
	if err != nil {
		logger.Info(fmt.Sprintf(`{"event":"backend.logger.error","error":%q}`, err.Error()))
		return
	}
	logger.Info(string(message))
}

func main() {}
