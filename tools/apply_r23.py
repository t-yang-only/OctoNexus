import io

p = "internal/server/handlers/setting.go"
s = io.open(p, encoding="utf-8").read()

old = '''\tcase model.SettingKeyRouteBalanceEnabled:
\t\t// 加权轮询热路径开关即时生效: relay 不耦合配置源, 由装配层在此注入。
\t\tenabled, err := strconv.ParseBool(setting.Value)
\t\tif err != nil {
\t\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\t\treturn
\t\t}
\t\trelay.SetRouteBalanceEnabled(enabled)'''

new = '''\tcase model.SettingKeyRouteBalanceEnabled:
\t\t// 加权轮询热路径开关即时生效: relay 不耦合配置源, 由装配层在此注入。
\t\tif enabled, err := strconv.ParseBool(setting.Value); err == nil {
\t\t\trelay.SetRouteBalanceEnabled(enabled)
\t\t}'''
assert old in s
s = s.replace(old, new, 1)

old2 = '''\tcase model.SettingKeyModelInfoUpdateInterval:
\t\thours, err := strconv.Atoi(setting.Value)
\t\tif err != nil {
\t\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\t\treturn
\t\t}
\t\ttask.Update(string(setting.Key), time.Duration(hours)*time.Hour)'''

new2 = '''\tcase model.SettingKeyModelInfoUpdateInterval:
\t\tif hours, err := strconv.Atoi(setting.Value); err == nil {
\t\t\ttask.Update(string(setting.Key), time.Duration(hours)*time.Hour)
\t\t}'''
assert old2 in s
s = s.replace(old2, new2, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)
print("R23_APPLIED")
