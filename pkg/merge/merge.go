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
			// } else {
			// 	// Both are non-objects (strings, arrays, numbers, etc.)
			// 	// dst already has the correct value since it has higher precedence
			// 	// No action needed - dst value is already in place
		}
	}
	return dst
}

func isObject(v interface{}) bool {
	_, ok := v.(map[string]interface{})
	return ok
}

// MergeMapsWithDeletion merges maps where desired is the source of truth.
// Keys that exist in existing but not in desired will be removed.
// Nested objects are recursively merged, but keys not in desired are still removed.
func MergeMapsWithDeletion(desired, existing map[string]interface{}, log *logger.Logger) (map[string]interface{}, error) {
	if desired == nil {
		return nil, nil
	}
	if existing == nil {
		// If no existing map, return a deep copy of desired
		desiredCopy, err := copystructure.Copy(desired)
		if err != nil {
			return nil, err
		}
		result, ok := desiredCopy.(map[string]interface{})
		if !ok {
			return nil, errors.New("failed to copy desired map")
		}
		return result, nil
	}

	// Start with a deep copy of desired
	desiredCopy, err := copystructure.Copy(desired)
	if err != nil {
		return nil, err
	}
	result, ok := desiredCopy.(map[string]interface{})
	if !ok {
		return nil, errors.New("failed to copy desired map")
	}

	// Recursively merge nested objects from existing into desired
	// This allows preserving nested values that exist in both, but
	// keys not in desired will be removed
	for key, desiredVal := range result {
		if existingVal, exists := existing[key]; exists {
			if desiredMap, ok := desiredVal.(map[string]interface{}); ok {
				if existingMap, ok := existingVal.(map[string]interface{}); ok {
					// Both are maps, recursively merge (desired takes precedence)
					merged, mergeErr := MergeMapsWithDeletion(desiredMap, existingMap, log)
					if mergeErr != nil {
						log.Debug().Err(mergeErr).Str("key", key).Msg("Failed to merge nested map, using desired")
					} else {
						result[key] = merged
					}
				}
				// If existing is not a map, keep desired value (desired takes precedence)
			}
			// If desired is not a map, keep desired value (desired takes precedence)
		}
		// If key doesn't exist in existing, keep desired value (already in result)
	}

	// Keys in existing but not in desired are implicitly removed by not being added to result
	return result, nil
}
