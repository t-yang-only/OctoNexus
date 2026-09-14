package notify

import "github.com/bestruirui/octopus/internal/model"

// opSettingGet 由 op 包注入, 避免 notify→op 的导入环 (op→…→notify 单向依赖)。
var opSettingGet = func(key model.SettingKey) (string, error) {
	return "", errSettingUnavailable
}

type errSettingUnavailableError struct{}

func (errSettingUnavailableError) Error() string { return "setting source not configured" }

var errSettingUnavailable = errSettingUnavailableError{}

// SetSettingSource 注入设置读取函数, 由装配层 (server) 启动时调用。
func SetSettingSource(get func(key model.SettingKey) (string, error)) {
	opSettingGet = get
}
