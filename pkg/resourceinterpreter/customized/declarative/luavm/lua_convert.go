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
	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/conversion"
	luajson "layeh.com/gopher-json"
)

// ConvertLuaResultInto convert lua result to obj
func ConvertLuaResultInto(luaResult lua.LValue, obj interface{}) error {
	_, err := conversion.EnforcePtr(obj)
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
	//   1. Walk the object (lua table), delete the key with empty value (`nodeClaim` in this example):
	//     {
	//         resourceRequest: {
	//             cpu: "100m"
	//         }
	//     }
	//   2. Encode the object with luajson to be:
	//     {"resourceRequest": {"cpu": "100m"}}
	//   4. Finally, unmarshal the new json to object, get
	//     {
	//         resourceRequest: {
	//             cpu: "100m"
	//         }
	//     }

	jsonBytes, err := luajson.Encode(luaResult)
	if err != nil {
		return fmt.Errorf("json Encode obj eroor %v", err)
	}

	jsonBytes = bytes.Replace(jsonBytes, []byte("[]"), []byte("{}"), -1)

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
