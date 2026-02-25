// Package logging provides logging utilities for quadsyncd.
package logging

import (
	"context"
	"encoding/json"
	"log/slog"
)

// TeeHandler is a slog.Handler that forwards logs to multiple handlers.
type TeeHandler struct {
	handlers []slog.Handler
}

// NewTeeHandler creates a handler that writes to all provided handlers.
func NewTeeHandler(handlers ...slog.Handler) *TeeHandler {
	return &TeeHandler{handlers: handlers}
}

// Enabled reports whether the handler handles records at the given level.
func (t *TeeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// If any handler is enabled for this level, we're enabled
	for _, h := range t.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle handles the record by forwarding it to all handlers.
func (t *TeeHandler) Handle(ctx context.Context, r slog.Record) error {
	var lastErr error
	for _, h := range t.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// WithAttrs returns a new handler with the given attributes added.
func (t *TeeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(t.handlers))
	for i, h := range t.handlers {
		newHandlers[i] = h.WithAttrs(attrs)
	}
	return &TeeHandler{handlers: newHandlers}
}

// WithGroup returns a new handler with the given group name added.
func (t *TeeHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(t.handlers))
	for i, h := range t.handlers {
		newHandlers[i] = h.WithGroup(name)
	}
	return &TeeHandler{handlers: newHandlers}
}

// NDJSONHandler is a handler that writes ndjson log lines via a callback.
type NDJSONHandler struct {
	level       slog.Level
	attrs       []slog.Attr
	groups      []string
	writeFunc   func([]byte) error
	addSource   bool
	replaceAttr func([]string, slog.Attr) slog.Attr
}

// NDJSONHandlerOptions configures an NDJSONHandler.
type NDJSONHandlerOptions struct {
	Level       slog.Level
	AddSource   bool
	ReplaceAttr func(groups []string, a slog.Attr) slog.Attr
}

// NewNDJSONHandler creates a handler that writes ndjson lines.
func NewNDJSONHandler(writeFunc func([]byte) error, opts *NDJSONHandlerOptions) *NDJSONHandler {
	if opts == nil {
		opts = &NDJSONHandlerOptions{}
	}
	level := slog.LevelInfo
	if opts.Level != 0 {
		level = opts.Level
	}
	return &NDJSONHandler{
		level:       level,
		writeFunc:   writeFunc,
		addSource:   opts.AddSource,
		replaceAttr: opts.ReplaceAttr,
	}
}

// Enabled reports whether the handler handles records at the given level.
func (h *NDJSONHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle handles the record by encoding it as ndjson and calling writeFunc.
func (h *NDJSONHandler) Handle(_ context.Context, r slog.Record) error {
	// Build a map with the log record
	m := make(map[string]interface{})

	// Standard fields
	m["time"] = r.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	m["level"] = r.Level.String()
	m["msg"] = r.Message

	// Add attributes from the handler's context
	for _, attr := range h.attrs {
		addAttrToMap(m, h.groups, attr, h.replaceAttr)
	}

	// Add attributes from the record
	r.Attrs(func(a slog.Attr) bool {
		addAttrToMap(m, h.groups, a, h.replaceAttr)
		return true
	})

	// Encode and write
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return h.writeFunc(data)
}

// WithAttrs returns a new handler with the given attributes added.
func (h *NDJSONHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	newAttrs = append(newAttrs, attrs...)
	return &NDJSONHandler{
		level:       h.level,
		attrs:       newAttrs,
		groups:      h.groups,
		writeFunc:   h.writeFunc,
		addSource:   h.addSource,
		replaceAttr: h.replaceAttr,
	}
}

// WithGroup returns a new handler with the given group name added.
func (h *NDJSONHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newGroups := make([]string, len(h.groups), len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups = append(newGroups, name)
	return &NDJSONHandler{
		level:       h.level,
		attrs:       h.attrs,
		groups:      newGroups,
		writeFunc:   h.writeFunc,
		addSource:   h.addSource,
		replaceAttr: h.replaceAttr,
	}
}

// addAttrToMap adds an attribute to the map, respecting groups.
func addAttrToMap(m map[string]interface{}, groups []string, a slog.Attr, replaceAttr func([]string, slog.Attr) slog.Attr) {
	if replaceAttr != nil {
		a = replaceAttr(groups, a)
	}
	if a.Equal(slog.Attr{}) {
		return
	}

	// Navigate to the correct nested map
	target := m
	for _, g := range groups {
		if _, ok := target[g]; !ok {
			target[g] = make(map[string]interface{})
		}
		target = target[g].(map[string]interface{})
	}

	// Add the attribute
	target[a.Key] = a.Value.Any()
}
