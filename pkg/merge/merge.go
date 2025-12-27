package merge

import (
	"github.com/mitchellh/copystructure"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
)

func MergeMaps(base, overwriteMap map[string]interface{}, log *logger.Logger) (map[string]interface{}, error) {
	if overwriteMap == nil {
		return base, nil
	}
	overwriteCopy, err := copystructure.Copy(overwriteMap)
	if err != nil {
		return nil, err
	}
	result, ok := overwriteCopy.(map[string]interface{})
	if !ok {
		return nil, errors.New("failed to merge maps")
	}

	for key, val := range base {
		if value, ok := result[key]; ok {
			if dest, ok := value.(map[string]interface{}); ok {
				// if result[key] is an object, merge overwriteMaps's val object into result[key].
				src, ok := val.(map[string]interface{})
				if !ok {
					// If the original value is nil, there is nothing to merge, so we don't print the warning
					if val != nil {
						log.Warn().Msgf("warning: skipped value for %s: Not a object.", key)
					}
				} else {
					mergeObject(dest, src, log)
				}
			}
		} else {
			// If the key is not in overwriteMap, copy it from base.
			result[key] = val
		}
	}
	return result, nil
}

func mergeObject(dst, src map[string]interface{}, log *logger.Logger) map[string]interface{} {
	if src == nil {
		return dst
	}
	if dst == nil {
		return src
	}
	// Because dst has higher precedence than src, dst values override src values.
	for key, val := range src {
		if dv, ok := dst[key]; !ok {
			// Key doesn't exist in dst, add it from src
			dst[key] = val
		} else if isObject(val) {
			if isObject(dv) {
				// Both are objects, recursively merge (dst has higher precedence)
				mergeObject(dv.(map[string]interface{}), val.(map[string]interface{}), log)
			} else {
				// src is object but dst is not, keep dst (dst has higher precedence)
				if val != nil {
					log.Debug().Msgf("keeping non-object value for %s from destination (destination has higher precedence)", key)
				}
			}
		} else if isObject(dv) {
			// dst is object but src is not, keep dst (dst has higher precedence)
			if val != nil {
				log.Debug().Msgf("keeping object value for %s from destination (destination has higher precedence)", key)
			}
		} else {
			// Both are non-objects (strings, arrays, numbers, etc.)
			// dst already has the correct value since it has higher precedence
			// No action needed - dst value is already in place
		}
	}
	return dst
}

func isObject(v interface{}) bool {
	_, ok := v.(map[string]interface{})
	return ok
}
