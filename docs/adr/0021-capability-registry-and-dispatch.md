# 能力注册与派发：`capabilities/` 目录即声明，派发统一入口并自动登记在途清单

能力真源现状是三处分散：云端能力在 `config/` 声明、给主控看的可用能力目录是手写文档、本地拓扑（常驻能力 agent 与 workspace 布局）硬编码在引导逻辑里。三者各自演进——新增一处能力要改多处，漏改就漂移：文档写了但平台没有（派活即失败），平台有了但文档没写（主控永远不知道），本地 agent 起了但引导不认识（重启即丢）。真人可读的那一层（每个能力的用途、何时用、说明文档在哪）尤其没有归属：平台声明只有机器字段，写不进「为什么用它」。

**决策：**

- **`super-agent/capabilities/` 目录即声明，目录结构即数据**：`agents/<key>/{capability.yaml,instructions.md}` + 可选 `skills.yaml`（该 agent 挂载哪些共享技能，见 0025 修订）、`skills/<key>/SKILL.md`（共享池；agent 专属放 `agents/<key>/skills/<key>/SKILL.md`）、`scripts/<key>.yaml`、`cloud/<key>.yaml` 四类；key 取目录名，不再有第二处清单。
- **字段沿用 x-deploy 词汇**（key/name/description 为基础），按类型扩：agent 加 role(master|retro|job)、workspace(master|crew|jobs)、kind、args、maxConcurrent；skill 用 frontmatter 的 name/description（不再有独立元数据文件，见修订条）；script 加 path；cloud 加 space、org、note。词汇一致是为了让人在两个仓库间读同一种声明不需要翻译。
- **主控与复盘者也是注册表条目**（role=master / retro）：本地拓扑由注册表派生，引导逻辑不再硬编码「哪些 agent、在哪个 workspace、常驻几个」。拓扑与声明同源，是漂移的根治法。
- **云端条目只写指针，真源仍是 `config/`**：`cloud/<key>.yaml` 指向 `config/hifox/agents/<key>` 或 `config/apifox/agents/<key>`，检查项校验指针目标存在。不在本目录复制云端声明——复制即两份真源。
- **运行时扫目录生成主控读的索引**：名字 + 一句话 + 何时用。主控只拿索引，细节按需读单篇——索引小到可以常驻，细节大到不该常驻。
- **技能是标准 `SKILL.md`，位置即归属**（2026-11 修订，见 ADR 0025）：`skills/<key>/SKILL.md` 共享、`agents/<key>/skills/<key>/SKILL.md` 专属，与 `config/` 的分法一致；真源目录直接进底层 CLI 的技能发现面（`skills.customDirectories`），不再有 `capability.yaml`/`doc.md`，也不复制副本。索引里不再给文档路径——给路径会诱导模型整篇读文件，绕过技能机制。
- **`superctl capabilities --check` 查漂移**：key 与目录名不符、master 缺失或重复、声明的文档缺失、脚本不可执行、cloud 指针在 `config/` 里找不到、`direct/` 里有脚本却没登记。漂移扫得出来，才有资格说「目录即真源」。
- **派发统一入口 `superctl dispatch --cloud|--local`**：云端先把注册表里的能力名解析成唯一平台 ref（平台名字带实例后缀，hifox 集合写作 `戴宗 (staging)`、apifox 集合写作 `燕青 - staging`，两种都要剥；声明名可以是平台基名的前缀，如 `巴德尔` 对 `巴德尔 Baldur`；注册表 key 不是 ref；歧义时必须 `--instance` 消歧或 `--delegate` 直通，绝不猜实例），`--space` 缺省取声明值，再包 `hifox task create`；本地按注册表的 agent 配方起 jobs agent 并注入任务。两条通道都自动登记在途清单（`watchlist.json` 改名扩义为 `inflight.json`，kind=cloud|local；哨兵只读 cloud 条目）并在台账活跃区建骨架（仅文件不存在时建，之后归主控写）。登记不靠主控自觉：靠自觉的登记迟早漏一条，漏的那条就是急停时看不见的在途件。

**Considered Options：**

- **单文件总清单**（一个 yaml 列所有能力）：拒。能力细节（instructions、文档）必须挂文件，单文件必然外挂一堆路径，等于把目录结构摊平写进一个文件，读它的人还得自己拼回来。
- **全进 `config/`，交 x-deploy 管**：拒。违反 ADR 0016：`config/` 是云端部署声明、经发布流水线；本地运行时物料进 `config/` 会被部署语义裹挟（改一条本地 agent 配方要过一次发布）。
- **不写云端条目，派发前现查 `hifox agent list`**：拒。现查只有平台字段，丢掉人类知识（何时用、用途、边界）；而且每次派发都要一次网络查询，查询失败就没有可用能力面。

**Consequences：**

- 主控拿到的是索引而非全量细节：能力变多时索引增长、细节不增长；代价是主控需要一次额外的文档读取。
- 本地声明与云端 `config/` 之间存在双写（指针 + 目标），靠 `capabilities --check` 兜住，不做自动同步。
- `AGENTS.md` 需补一条：`capabilities/` 的消费者是本地主控，**不部署**——避免下一位维护者按目录位置误判它是部署声明。
