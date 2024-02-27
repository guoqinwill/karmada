/*
Copyright 2022 The Karmada Authors.

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

package luavm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"

	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/klog/v2"
	luajson "layeh.com/gopher-json"
)

// luajsonFormatRegexp a regular expression which used to format the string encoded by luajson.
var luajsonFormatRegexp *regexp.Regexp

func init() {
	var err error
	luajsonFormatRegexp, err = regexp.Compile(`"([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]":\[]`)
	if err != nil {
		klog.Errorf("Failed to init regexp, invalid regexp: %+v", err)
	}
}

// ConvertLuaResultInto convert lua result to obj
func ConvertLuaResultInto(luaResult lua.LValue, obj interface{}) error {
	t, err := conversion.EnforcePtr(obj)
	if err != nil {
		return fmt.Errorf("obj is not pointer")
	}

	// For example, `GetReplicas` returns requirement with empty:
	//     {
	//         nodeClaim: {},
	//         resourceRequest: {
	//             cpu: "100m"
	//         }
	//     }
	// Luajson encodes it to
	//     {"nodeClaim": [], "resourceRequest": {"cpu": "100m"}}
	//
	// While go json fails to unmarshal `[]` to ReplicaRequirements.NodeClaim object.
	// ReplicaRequirements object.
	//
	// Here we handle it as follows:
	//   1. Encode the object with luajson to be:
	//     {"nodeClaim": [], "resourceRequest": {"cpu": "100m"}}
	//   2. replace the empty value from `[]` to `{}` (`nodeClaim` in this example)
	//     {"nodeClaim": {}, "resourceRequest": {"cpu": "100m"}}
	//   3. Finally, unmarshal the new json to object, get
	//     {
	//         nodeClaim: {},
	//         resourceRequest: {
	//             cpu: "100m"
	//         }
	//     }
	//
	//  Notes: don't worry that the value of a field is `[]`, and it originally represents an empty slice.
	//  Because this situation will not appear in the following `jsonBytes`.
	//  Supposing the value of a field is originally an empty slice:
	//    1. if this field is omitempty, this empty slice value and its key will both been removed in marshal process.
	//       e.g: `Rules.Resources` field in `Role` Type.
	//    2. if this field is not omitempty, but required, empty slice is impossible to be created.
	//       e.g: `Rules.Verbs` field in `Role` Type.
	//    3. if this field is not omitempty, but optional, it has been verified also be removed in `jsonBytes`.
	//       e.g: `Rules` field in `Role` Type.
	jsonBytes, err := luajson.Encode(luaResult)
	if err != nil {
		return fmt.Errorf("json Encode obj error %v", err)
	}

	// Only if `[]` is the value of a field, it will be converted to `{}`,
	// otherwise, for example, if `[]` is a substring in a string value, it needs to be ignored
	//
	// e.g: assuming the given jsonBytes is: {"one-field":[],"one-label":"\"hello\":[]"}
	// it is expected to convert to: {"one-field":{},"one-label":"\"hello\":[]"}
	jsonBytes = luajsonFormatRegexp.ReplaceAllFunc(jsonBytes, func(src []byte) []byte {
		dst := bytes.ReplaceAll(src, []byte(`[]`), []byte(`{}`))
		return dst
	})

	//  for lua an empty object by json encode be [] not {}
	if t.Kind() == reflect.Struct && len(jsonBytes) > 1 && jsonBytes[0] == '[' {
		jsonBytes[0], jsonBytes[len(jsonBytes)-1] = '{', '}'
	}

	err = json.Unmarshal(jsonBytes, obj)
	if err != nil {
		return fmt.Errorf("can not unmarshal %v to %#v：%v", string(jsonBytes), obj, err)
	}
	return nil
}

// ConvertLuaResultToInt convert lua result to int.
func ConvertLuaResultToInt(luaResult lua.LValue) (int32, error) {
	if luaResult.Type() != lua.LTNumber {
		return 0, fmt.Errorf("result type %#v is not number", luaResult.Type())
	}
	return int32(luaResult.(lua.LNumber)), nil
}

// ConvertLuaResultToBool convert lua result to bool.
func ConvertLuaResultToBool(luaResult lua.LValue) (bool, error) {
	if luaResult.Type() != lua.LTBool {
		return false, fmt.Errorf("result type %#v is not bool", luaResult.Type())
	}
	return bool(luaResult.(lua.LBool)), nil
}
