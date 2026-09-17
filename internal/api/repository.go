/**
 * [INPUT]: 依赖 client.go 的 Client.do / notFoundCode / ErrNotFound、fmt、net/url，依赖 internal/build 的 Version
 * [OUTPUT]: 对外提供 CodeRepo / CodeRepoEnv / CodeRepoMeta / CodeRepoProperties / CodeRepoResource（含 AppRole）类型、
 *           Client.CreateRepository(appKey) 方法、CodeRepoResource.CloneURL() 收口方法（仓库按 app 划分，环境 → app 由 App.KeyForEnv 定位）
 * [POS]: internal/api 的代码仓库服务（make-repo）调用层，POST /code/v1/repository?version=<cli 版本>，
 *        经 X-Make-Target 区分动作；与 client.go 的 Meta 操作共用 Client 与 do 原语。
 *        version 查询参数是服务端强制升级门禁的依据（缺失即拒绝并提示 makecli update）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"fmt"
	"net/url"

	"github.com/qfeius/makecli/internal/build"
)

// codeRepoType 是代码仓库资源的固定 type 标识
const codeRepoType = "Make.Code.Repository"

// codeRepoPath 是代码仓库端点，version 查询参数带上 CLI 版本供服务端做升级门禁：
// 服务端按 version 缺失（后续按具体版本）拒绝请求并提示 makecli update
func codeRepoPath() string {
	return "/code/v1/repository?version=" + url.QueryEscape(build.Version)
}

// CodeRepo 描述单个远端仓库（repoName / cloneUrl；MakeRepoID 映射服务端 giteaRepoId 字段——
// json tag 是线上契约不可改，Go 字段名去耦底层实现）
type CodeRepo struct {
	RepoName   string `json:"repoName"`
	MakeRepoID int64  `json:"giteaRepoId"`
	CloneURL   string `json:"cloneUrl"`
}

// CodeRepoEnv 是响应的 properties.env 段：一个 app 只承载一个环境，故只有一个 repository
type CodeRepoEnv struct {
	Repository CodeRepo `json:"repository"`
}

// CodeRepoMeta 是响应的 meta 段；CloneURL 为历史单仓库形态的兼容字段
type CodeRepoMeta struct {
	Version  string `json:"version"`
	Owner    string `json:"owner"`
	CloneURL string `json:"cloneUrl"`
}

// CodeRepoProperties 是响应的 properties 段
type CodeRepoProperties struct {
	OrgID        int64       `json:"orgId"`
	Private      bool        `json:"private"`
	CreatedOrg   bool        `json:"createdOrg"`
	CreatedRepos []string    `json:"createdRepos"`
	Env          CodeRepoEnv `json:"env"`
}

// CodeRepoResource 是 /code/v1/repository 各动作返回的 data 段。
// 仓库按 app 而非按环境划分：product / beta 配对各自一个仓库，环境 → app 的定位由 App.KeyForEnv 完成，
// 本资源只回答「这个 app 的仓库在哪」
type CodeRepoResource struct {
	AppKey     string             `json:"appKey"`
	AppRole    string             `json:"appRole"`
	Type       string             `json:"type"`
	Meta       CodeRepoMeta       `json:"meta"`
	Properties CodeRepoProperties `json:"properties"`
}

// CloneURL 返回本 app 仓库的推送地址：properties.env.repository.cloneUrl，缺失退回历史 meta.cloneUrl；
// 都没有返回空串，由调用方决定如何报错
func (r *CodeRepoResource) CloneURL() string {
	if u := r.Properties.Env.Repository.CloneURL; u != "" {
		return u
	}
	return r.Meta.CloneURL
}

// CreateRepository 调用 MakeService.CreateResource 按租户幂等准备代码仓库：
// Organization / Repository 不存在则创建，存在则复用。成功即代表仓库已就绪，可以 git push。
// 请求携带 version 查询参数（CLI 版本），服务端据此做强制升级判定。
func (c *Client) CreateRepository(appKey string) (*CodeRepoResource, error) {
	body := map[string]any{
		"type":   codeRepoType,
		"appKey": appKey,
	}
	var result struct {
		Code    int              `json:"code"`
		Message string           `json:"msg"`
		Data    CodeRepoResource `json:"data"`
	}
	if err := c.do("MakeService.CreateResource", codeRepoPath(), body, &result); err != nil {
		return nil, err
	}
	if result.Code == notFoundCode {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, result.Message)
	}
	if result.Code != 200 {
		return nil, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return &result.Data, nil
}
