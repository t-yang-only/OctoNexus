[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$msg = @"
feat(group): Round 37 一键建组与渠道/分组读操作 404 语义收敛

R37-F1(HIGH): groupQuickCreate 在引用的渠道不存在时准确返回 404 而非 400。
  此前 ChannelGet 在未命中缓存时仅返回普通 error，导致 groupQuickCreate 无论遇到何种错误一律返回 400 BadRequest，
  将“目标渠道不存在”误报为请求输入格式错误，破坏了 RESTful 契约。
  现通过 ChannelGet 返回 op.NotFoundf，并在 groupQuickCreate 中对 op.IsNotFound 进行优先 404 分流。
R37-F2(MEDIUM): getGroup / verifyChannelModels / checkChannelUpstream 统一接入 writeOpError。
  此前直接返回 resp.Error(c, http.StatusNotFound, err.Error())，一旦未来出现底层系统级错误可能误报 404；
  现统一由 writeOpError 收口，严格分流 404 与 500。
测试：独立单元用例 TestGroupQuickCreateReturns404WhenChannelNotFound、TestGetGroupReturns404WhenNotFound 与 TestVerifyChannelModelsReturns404WhenChannelNotFound PASS；
变异：Mutation R37-F1 精准杀灭（改回 400 测试当场红并被 RC=1 拦截）；
回归：全仓 21 包 0 FAIL；前端检查全绿。
"@
git commit -m $msg
git log --oneline -1
