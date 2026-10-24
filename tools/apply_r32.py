import io

p = "internal/op/manual_subscription.go"
s = io.open(p, encoding="utf-8").read()

old1 = 'return model.ManualSubscription{}, fmt.Errorf("manual subscription %d not found", id)'
new1 = 'return model.ManualSubscription{}, NotFoundf("manual subscription %d not found", id)'
assert old1 in s
s = s.replace(old1, new1, 1)

old2 = """func ManualSubscriptionDelete(ctx context.Context, id int) error {
\tif _, err := ManualSubscriptionGet(id); err != nil {
\t\treturn err
\t}
\tif err := db.GetDB().WithContext(ctx).Delete(&model.ManualSubscription{}, id).Error; err != nil {
\t\treturn fmt.Errorf("failed to delete manual subscription: %w", err)
\t}
\treturn ManualSubscriptionRefresh(ctx)
}"""

new2 = """func ManualSubscriptionDelete(ctx context.Context, id int) error {
\tif _, err := ManualSubscriptionGet(id); err != nil {
\t\treturn err
\t}
\tres := db.GetDB().WithContext(ctx).Delete(&model.ManualSubscription{}, id)
\tif res.Error != nil {
\t\treturn fmt.Errorf("failed to delete manual subscription: %w", res.Error)
\t}
\tif res.RowsAffected == 0 {
\t\treturn NotFoundf("manual subscription %d not found", id)
\t}
\treturn ManualSubscriptionRefresh(ctx)
}"""
assert old2 in s
s = s.replace(old2, new2, 1)
io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/subscription.go"
s2 = io.open(p2, encoding="utf-8").read()

old_up = """\titem, err := op.ManualSubscriptionUpdate(c.Request.Context(), req)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_up = """\titem, err := op.ManualSubscriptionUpdate(c.Request.Context(), req)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_up in s2
s2 = s2.replace(old_up, new_up, 1)

old_del = """\tif err := op.ManualSubscriptionDelete(c.Request.Context(), id); err != nil {
\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\treturn
\t}"""

new_del = """\tif err := op.ManualSubscriptionDelete(c.Request.Context(), id); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_del in s2
s2 = s2.replace(old_del, new_del, 1)
io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R32_APPLIED")
