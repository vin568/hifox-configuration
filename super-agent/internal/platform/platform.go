// Package platform 给「平台」一个不属于任何 CLI 适配包的落点：平台 = 本机派活的
// 远端执行面，取值为平台 CLI 的应用名（能力指针目录 capabilities/platforms/<平台>/、
// 派活 --platform <平台>、在途清单的 platform 字段都用它）。
//
// 单独成包是为了分层：状态包（inflight/sentinel）与能力对账只需要这个名字，
// 不该为了一个常量反向依赖 internal/hifox（那会让状态层跟着某个平台的 CLI 适配一起动）。
package platform

// Hifox 是 hifox 平台 CLI 的应用名；将来还有兄弟（如 sharkly）。
const Hifox = "hifox"
