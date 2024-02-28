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
	"encoding/json"
	"fmt"
	"k8s.io/klog/v2"

	"github.com/yuin/gluamapper"
	lua "github.com/yuin/gopher-lua"
)

// ConvertLuaResultInto convert lua result to obj
func ConvertLuaResultInto(luaResult lua.LValue, obj interface{}) error {
	err := gluamapper.Map(luaResult.(*lua.LTable), obj)
	klog.Infof("[DEBUG] obj: %+v, err: %+v", obj, err)
	buf, err2 := json.Marshal(obj)
	klog.Infof("[DEBUG] json: %s, err: %+v", buf, err2)
	return err
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
