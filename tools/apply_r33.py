import io

p = "internal/op/alert_rule.go"
s = io.open(p, encoding="utf-8").read()

old_imp = 'import (\n\t"context"'
new_imp = 'import (\n\t"context"\n\t"errors"'
assert old_imp in s
s = s.replace(old_imp, new_imp, 1)

old_imp2 = '"github.com/t-yang-only/OctoNexus/internal/model"\n)'
new_imp2 = '"github.com/t-yang-only/OctoNexus/internal/model"\n\t"gorm.io/gorm"\n)'
assert old_imp2 in s
s = s.replace(old_imp2, new_imp2, 1)

old_get = """func AlertRuleGet(ctx context.Context, id int) (*model.AlertRule, error) {
\tvar item model.AlertRule
\tif err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
\t\treturn nil, fmt.Errorf("get alert rule %d: %w", id, err)
\t}
\treturn &item, nil
}"""

new_get = """func AlertRuleGet(ctx context.Context, id int) (*model.AlertRule, error) {
\tvar item model.AlertRule
\tif err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
\t\tif errors.Is(err, gorm.ErrRecordNotFound) {
\t\t\treturn nil, NotFoundf("alert rule %d not found", id)
\t\t}
\t\treturn nil, fmt.Errorf("get alert rule %d: %w", id, err)
\t}
\treturn &item, nil
}"""
assert old_get in s
s = s.replace(old_get, new_get, 1)

old_del = """func AlertRuleDelete(ctx context.Context, id int) error {
\tif err := db.GetDB().WithContext(ctx).Delete(&model.AlertRule{}, id).Error; err != nil {
\t\treturn fmt.Errorf("delete alert rule %d: %w", id, err)
\t}
\treturn nil
}"""

new_del = """func AlertRuleDelete(ctx context.Context, id int) error {
\tres := db.GetDB().WithContext(ctx).Delete(&model.AlertRule{}, id)
\tif res.Error != nil {
\t\treturn fmt.Errorf("delete alert rule %d: %w", id, res.Error)
\t}
\tif res.RowsAffected == 0 {
\t\treturn NotFoundf("alert rule %d not found", id)
\t}
\treturn nil
}"""
assert old_del in s
s = s.replace(old_del, new_del, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/alert_rule.go"
s2 = io.open(p2, encoding="utf-8").read()

old_up = """\titem, err := op.AlertRuleUpdate(c.Request.Context(), id, &req)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_up = """\titem, err := op.AlertRuleUpdate(c.Request.Context(), id, &req)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_up in s2
s2 = s2.replace(old_up, new_up, 1)

old_del2 = """\tif err := op.AlertRuleDelete(c.Request.Context(), id); err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_del2 = """\tif err := op.AlertRuleDelete(c.Request.Context(), id); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_del2 in s2
s2 = s2.replace(old_del2, new_del2, 1)

old_eval = """\trule, err := op.AlertRuleGet(c.Request.Context(), body.RuleID)
\tif err != nil {
\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\treturn
\t}"""

new_eval = """\trule, err := op.AlertRuleGet(c.Request.Context(), body.RuleID)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_eval in s2
s2 = s2.replace(old_eval, new_eval, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R33_APPLIED")
