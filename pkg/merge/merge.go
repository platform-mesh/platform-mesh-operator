package merge

import (
	"github.com/mitchellh/copystructure"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
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
	for key, val := range dst {
		if val == nil {
			src[key] = nil
		}
	}
	// Because dst has higher precedence than src, dst values override src values.
	for key, val := range src {
		if dv, ok := dst[key]; !ok {
			dst[key] = val
		} else if isObject(val) {
			if isObject(dv) {
				mergeObject(dv.(map[string]interface{}), val.(map[string]interface{}), log)
			} else {
				log.Warn().Msgf("warning: cannot overwrite object with non object for %s (%v)", key, val)
			}
		} else if isObject(dv) && val != nil {
			log.Warn().Msgf("warning: destination for %s is a object. Ignoring non-object value (%v)", key, val)
		}
	}
	return dst
}

func isObject(v interface{}) bool {
	_, ok := v.(map[string]interface{})
	return ok
}
