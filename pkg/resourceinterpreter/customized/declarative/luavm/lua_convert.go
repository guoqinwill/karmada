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
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	_ "github.com/tidwall/sjson"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
	"strings"

	"github.com/yuin/gluamapper"
	lua "github.com/yuin/gopher-lua"
)

// ConvertLuaResultToStruct convert lua result to a struct object
func ConvertLuaResultToStruct(luaResult *lua.LTable, obj interface{}) error {
	return gluamapper.Map(luaResult, obj)
}

// ConvertLuaResultToUnstruct convert lua result to unstructured.Unstructured{}
func ConvertLuaResultToUnstruct(luaResult *lua.LTable, references []*unstructured.Unstructured) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	err := gluamapper.Map(luaResult, &obj.Object)
	if err != nil {
		klog.Errorf("Convert lua result to unstructured failed, gluamapper.Map failed: %+v", err)
		return obj, err
	}

	klog.Infof("[DEBUG] gluamapper.Map: %+v", obj)

	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return obj, fmt.Errorf("json marshal obj failed: %+v", err)
	}

	klog.Infof("[DEBUG] marshal jsonBytes: %+v", obj)

	jsonBytes, err = convertEmptyMapBackToEmptySlice(jsonBytes, references)
	if err != nil {
		return obj, fmt.Errorf("convert empty map back to empty slice failed: %+v", err)
	}

	klog.Infof("[DEBUG] convertEmptyMapBackToEmptySlice: %+v", obj)

	err = json.Unmarshal(jsonBytes, obj)
	if err != nil {
		return obj, fmt.Errorf("json unmarshal obj failed: %+v", err)
	}

	klog.Infof("[DEBUG] unmarshal obj: %+v", obj)

	return obj, nil
}

func convertEmptyMapBackToEmptySlice(objBytes []byte, references []*unstructured.Unstructured) ([]byte, error) {
	objJsonStr := string(objBytes)
	fieldOfEmptySlice, fieldOfEmptyObject := make(map[string]struct{}), make(map[string]struct{})

	for _, reference := range references {
		jsonBytes, err := json.Marshal(reference)
		if err != nil {
			return objBytes, fmt.Errorf("json marshal reference failed: %+v", err)
		}
		jsonVal := gjson.Parse(string(jsonBytes))

		traverseToFindEmptyField(jsonVal, nil, fieldOfEmptySlice, fieldOfEmptyObject)
	}

	objJson := gjson.Parse(objJsonStr)
	fieldOfEmptyObjToSlice, fieldOfEmptyObjToDelete := make(map[string]struct{}), make(map[string]struct{})

	traverseToFindEmptyFieldNeededModify(objJson, nil, nil, fieldOfEmptySlice,
		fieldOfEmptyObject, fieldOfEmptyObjToSlice, fieldOfEmptyObjToDelete)

	var err error
	for fieldPath := range fieldOfEmptyObjToSlice {
		objJsonStr, err = sjson.Set(objJsonStr, fieldPath, []string{})
		if err != nil {
			return objBytes, fmt.Errorf("sjson set empty object to empty slice failed: %+v", err)
		}
	}
	for fieldPath := range fieldOfEmptyObjToDelete {
		objJsonStr, err = sjson.Delete(objJsonStr, fieldPath)
		if err != nil {
			return objBytes, fmt.Errorf("sjson delete empty object field failed: %+v", err)
		}
	}

	return []byte(objJsonStr), nil
}

func traverseToFindEmptyField(root gjson.Result, fieldPath []string, fieldOfEmptySlice, fieldOfEmptyObject map[string]struct{}) {
	rootIsNotArray := !root.IsArray()
	root.ForEach(func(key, value gjson.Result) bool {
		curFieldPath := fieldPath
		if rootIsNotArray {
			curFieldPath = append(fieldPath, key.String())
		}
		curFieldStr := strings.Join(curFieldPath, ".")

		if value.IsArray() && len(value.Array()) == 0 {
			fieldOfEmptySlice[curFieldStr] = struct{}{}
		} else if value.IsObject() && len(value.Map()) == 0 {
			fieldOfEmptyObject[curFieldStr] = struct{}{}
		} else if value.IsArray() || value.IsObject() {
			traverseToFindEmptyField(value, curFieldPath, fieldOfEmptySlice, fieldOfEmptyObject)
		}

		return true // keep iterating
	})
}

func traverseToFindEmptyFieldNeededModify(root gjson.Result, fieldPath, fieldPathWithArrayIndex []string, fieldOfEmptySlice, fieldOfEmptyObject,
	fieldOfEmptyObjToSlice, fieldOfEmptyObjToDelete map[string]struct{}) {
	rootIsNotArray := !root.IsArray()
	root.ForEach(func(key, value gjson.Result) bool {
		curFieldPath := fieldPath
		if rootIsNotArray {
			curFieldPath = append(fieldPath, key.String())
		}
		curFieldPathWithArrayIndex := append(fieldPathWithArrayIndex, key.String())

		if value.IsObject() && len(value.Map()) == 0 {
			curFieldPathStr := strings.Join(curFieldPath, ".")
			curFieldPathWithIndexStr := strings.Join(curFieldPathWithArrayIndex, ".")

			if _, ok := fieldOfEmptySlice[curFieldPathStr]; ok {
				fieldOfEmptyObjToSlice[curFieldPathWithIndexStr] = struct{}{}
			} else if _, ok := fieldOfEmptyObject[curFieldPathStr]; !ok {
				fieldOfEmptyObjToDelete[curFieldPathWithIndexStr] = struct{}{}
			}
		} else if value.IsArray() || value.IsObject() {
			traverseToFindEmptyFieldNeededModify(value, curFieldPath, curFieldPathWithArrayIndex, fieldOfEmptySlice,
				fieldOfEmptyObject, fieldOfEmptyObjToSlice, fieldOfEmptyObjToDelete)
		}

		return true // keep iterating
	})
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
