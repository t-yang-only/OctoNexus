import io

p = "internal/op/setting.go"
s = io.open(p, encoding="utf-8").read()

old_get_str = """func SettingGetString(key model.SettingKey) (string, error) {
\tsetting, ok := settingCache.Get(key)
\tif !ok {
\t\treturn "", fmt.Errorf("setting not found")
\t}
\treturn setting, nil
}"""

new_get_str = """func SettingGetString(key model.SettingKey) (string, error) {
\tsetting, ok := settingCache.Get(key)
\tif !ok {
\t\treturn "", NotFoundf("setting %q not found", key)
\t}
\treturn setting, nil
}"""
assert old_get_str in s
s = s.replace(old_get_str, new_get_str, 1)

old_set_str = """func SettingSetString(key model.SettingKey, value string) error {
\tvalueCache, ok := settingCache.Get(key)
\tif !ok {
\t\treturn fmt.Errorf("setting not found")
\t}
\tif valueCache == value {
\t\treturn nil
\t}
\tresult := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
\tif result.Error != nil {
\t\treturn fmt.Errorf("failed to set setting: %w", result.Error)
\t}
\tif result.RowsAffected == 0 {
\t\treturn fmt.Errorf("failed to set setting, key not found")
\t}
\tsettingCache.Set(key, value)
\treturn nil
}"""

new_set_str = """func SettingSetString(key model.SettingKey, value string) error {
\tvalueCache, ok := settingCache.Get(key)
\tif !ok {
\t\treturn NotFoundf("setting %q not found", key)
\t}
\tif valueCache == value {
\t\treturn nil
\t}
\tresult := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
\tif result.Error != nil {
\t\treturn fmt.Errorf("failed to set setting: %w", result.Error)
\t}
\tif result.RowsAffected == 0 {
\t\treturn NotFoundf("setting %q not found", key)
\t}
\tsettingCache.Set(key, value)
\treturn nil
}"""
assert old_set_str in s
s = s.replace(old_set_str, new_set_str, 1)

old_get_int = """func SettingGetInt(key model.SettingKey) (int, error) {
\tsetting, ok := settingCache.Get(key)
\tif !ok {
\t\treturn 0, fmt.Errorf("setting not found")
\t}
\treturn strconv.Atoi(setting)
}"""

new_get_int = """func SettingGetInt(key model.SettingKey) (int, error) {
\tsetting, ok := settingCache.Get(key)
\tif !ok {
\t\treturn 0, NotFoundf("setting %q not found", key)
\t}
\treturn strconv.Atoi(setting)
}"""
assert old_get_int in s
s = s.replace(old_get_int, new_get_int, 1)

old_get_bool = """func SettingGetBool(key model.SettingKey) (bool, error) {
\tsetting, ok := settingCache.Get(key)
\tif !ok {
\t\treturn false, fmt.Errorf("setting not found")
\t}
\treturn strconv.ParseBool(setting)
}"""

new_get_bool = """func SettingGetBool(key model.SettingKey) (bool, error) {
\tsetting, ok := settingCache.Get(key)
\tif !ok {
\t\treturn false, NotFoundf("setting %q not found", key)
\t}
\treturn strconv.ParseBool(setting)
}"""
assert old_get_bool in s
s = s.replace(old_get_bool, new_get_bool, 1)

old_set_int = """func SettingSetInt(key model.SettingKey, value int) error {
\tvalueCache, ok := settingCache.Get(key)
\tif !ok {
\t\treturn fmt.Errorf("setting not found")
\t}
\tvalueCacheNum, err := strconv.Atoi(valueCache)
\tif err != nil {
\t\treturn fmt.Errorf("failed to set setting: %w", err)
\t}
\tif valueCacheNum == value {
\t\treturn nil
\t}
\tresult := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
\tif result.Error != nil {
\t\treturn fmt.Errorf("failed to set setting: %w", result.Error)
\t}
\tif result.RowsAffected == 0 {
\t\treturn fmt.Errorf("failed to set setting, key not found")
\t}
\tsettingCache.Set(key, strconv.Itoa(value))
\treturn nil
}"""

new_set_int = """func SettingSetInt(key model.SettingKey, value int) error {
\tvalueCache, ok := settingCache.Get(key)
\tif !ok {
\t\treturn NotFoundf("setting %q not found", key)
\t}
\tvalueCacheNum, err := strconv.Atoi(valueCache)
\tif err != nil {
\t\treturn fmt.Errorf("failed to set setting: %w", err)
\t}
\tif valueCacheNum == value {
\t\treturn nil
\t}
\tresult := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
\tif result.Error != nil {
\t\treturn fmt.Errorf("failed to set setting: %w", result.Error)
\t}
\tif result.RowsAffected == 0 {
\t\treturn NotFoundf("setting %q not found", key)
\t}
\tsettingCache.Set(key, strconv.Itoa(value))
\treturn nil
}"""
assert old_set_int in s
s = s.replace(old_set_int, new_set_int, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/setting.go"
s2 = io.open(p2, encoding="utf-8").read()

old_h = """\tif err := op.SettingSetString(setting.Key, setting.Value); err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}"""

new_h = """\tif err := op.SettingSetString(setting.Key, setting.Value); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_h in s2
s2 = s2.replace(old_h, new_h, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R38_APPLIED")
