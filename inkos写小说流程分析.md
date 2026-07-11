# InkOS 写小说流程分析文档

> 基于 inkos 项目源码（`packages/core`）深度分析，仅聚焦"写小说"相关流程，不含翻译/互动影游/Play 开放世界等非写作功能。

---

## 目录

- [一、主流程概览](#一主流程概览)
- [二、建书主流程（initBook）](#二建书主流程initbook)
- [三、写下一章主流程（writeNextChapter）](#三写下一章主流程writenextchapter)
- [四、分支流程](#四分支流程)
  - [4.1 建书骨架流程（Architect + Foundation Reviewer）](#41-建书骨架流程architect--foundation-reviewer)
  - [4.2 章节规划流程（Planner）](#42-章节规划流程planner)
  - [4.3 上下文组装流程（Composer）](#43-上下文组装流程composer)
  - [4.4 章节写作流程（Writer + Settler）](#44-章节写作流程writer--settler)
  - [4.5 章节审查循环流程（Chapter Review Cycle）](#45-章节审查循环流程chapter-review-cycle)
  - [4.6 回炉/重写流程（Reviser）](#46-回炉重写流程reviser)
  - [4.7 润色流程（Polisher）](#47-润色流程polisher)
  - [4.8 导入续写流程（Import Chapters）](#48-导入续写流程import-chapters)
  - [4.9 卷级合并流程（Consolidator）](#49-卷级合并流程consolidator)
  - [4.10 短篇生产流程（Short Fiction Runner）](#410-短篇生产流程short-fiction-runner)
  - [4.11 自动写作/定时调度流程（Scheduler）](#411-自动写作定时调度流程scheduler)
  - [4.12 状态校验与修复流程](#412-状态校验与修复流程)
  - [4.13 AIGC 检测与反检测流程](#413-aigc-检测与反检测流程)
  - [4.14 基础设定修订流程（reviseFoundation）](#414-基础设定修订流程revisefoundation)
- [五、核心数据模型与文件结构](#五核心数据模型与文件结构)
- [六、关键源码文件索引](#六关键源码文件索引)

---

## 一、主流程概览

InkOS 的写小说流程围绕 **PipelineRunner**（`packages/core/src/pipeline/runner.ts`）展开，它是整个写作流水线的核心编排器。主流程可分为两条主线：

```
┌─────────────────────────────────────────────────────────────────────┐
│                        写小说主流程                                   │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ① 建书 (initBook)                                                  │
│     Architect 生成基础设定 → Foundation Reviewer 审查 → 落盘文件      │
│                                                                     │
│  ② 写下一章 (writeNextChapter) ← 可循环调用                           │
│     规划(Planner) → 组装(Composer) → 写作(Writer) →                  │
│     审查循环(Audit→Revise→Re-audit) → 状态结算 → 持久化              │
│                                                                     │
│  ③ 卷级合并 (Consolidator) ← 每卷完成后触发                           │
│     压缩章节摘要 → 卷级叙事段落 → 伏笔晋升                            │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

**三层交互入口**（CLI / TUI / Studio Web）统一通过 `interaction/runtime.ts` 的 action surface 调用 PipelineRunner，核心 action 包括：
- `create_book` → initBook
- `write_next` / `continue_book` → writeNextChapter
- `revise_chapter` / `rewrite_chapter` → reviseDraft
- `patch_chapter_text` → patchChapterText

---

## 二、建书主流程（initBook）

**入口**：`PipelineRunner.initBook(book, options)`
**源码**：`pipeline/runner.ts:706`

```
initBook(book, options)
  │
  ├─ 1. ArchitectAgent.generateFoundation(book, externalContext, reviewFeedback)
  │     └─ 生成 5 个 SECTION：story_frame / volume_map / roles / book_rules / pending_hooks
  │        └─ parseSectionsWithRepair() — 解析失败自动修复重试
  │
  ├─ 2. generateAndReviewFoundation() — 审查循环
  │     └─ FoundationReviewerAgent.review(foundation)
  │        ├─ 通过(≥80分) → 继续
  │        └─ 未通过 → 将审核反馈注入 reviewFeedback，重新生成（最多重试 2 次）
  │
  ├─ 3. ArchitectAgent.writeFoundationFiles(stagingBookDir, foundation)
  │     └─ 落盘文件：
  │        ├─ story/outline/story_frame.md     — 主题/冲突/世界观/终局
  │        ├─ story/outline/volume_map.md      — 分卷地图+节奏原则
  │        ├─ story/roles/主要角色/<name>.md   — 一人一卡（主角弧线权威）
  │        ├─ story/roles/次要角色/<name>.md
  │        ├─ story/book_rules.md              — 可执行规则卡
  │        ├─ story/current_state.md           — 种子占位（运行时追加）
  │        ├─ story/pending_hooks.md           — 初始伏笔池
  │        ├─ story/emotional_arcs.md          — 情感弧线（空表）
  │        ├─ story/story_bible.md             — 兼容指针
  │        └─ story/character_matrix.md        — 兼容指针
  │
  ├─ 4. StateManager.ensureControlDocumentsAt()
  │     ├─ story/author_intent.md   — 作者意图（长期方向）
  │     └─ story/current_focus.md   — 当前聚焦（近期方向）
  │
  ├─ 5. StateManager.saveChapterIndexAt([]) — 初始化章节索引
  ├─ 6. StateManager.snapshotStateAt(0)     — 创建初始状态快照
  │
  └─ 7. rename(stagingBookDir → bookDir)    — 原子性落盘
```

**关键点**：
- 基础设定采用**散文密度**设计，不使用表格/bullet，决定了后续 planner 能否读出稀疏 memo
- **OKR 递归大纲**：全书 Objective → 每卷 Objective + 3 个 Key Results，planner 据此按每 3-5 章推进一个 KR
- **前台/后台双层故事**：story_frame 必须明确写出前台冲突 + 后台暗线，两者因果咬合
- **伏笔池**含 Phase 7 扩展列（depends_on / pays_off_in_arc / core_hook / half_life）

---

## 三、写下一章主流程（writeNextChapter）

**入口**：`PipelineRunner.writeNextChapter(bookId, wordCount?, temperatureOverride?)`
**源码**：`pipeline/runner.ts:1665`

```
writeNextChapter(bookId)
  │
  ├─ 0. acquireBookLock(bookId) — 获取书籍写锁（进程级，心跳续约）
  │
  ├─ 1. prepareWriteInput(book, bookDir, chapterNumber, externalContext)
  │     │
  │     ├─ PlannerAgent.planChapter()          — Phase 3 规划
  │     │   ├─ loadPlanningSeedMaterials()     — 读取大纲/当前状态/作者意图
  │     │   ├─ findOutlineNode()               — 定位当前章在大纲中的位置
  │     │   ├─ deriveGoal()                    — 推导章节目标
  │     │   ├─ ChapterIntentSchema.parse()     — 构建确定性 Intent
  │     │   ├─ planChapterMemo()               — LLM 生成 7 段 ChapterMemo（最多重试3次）
  │     │   └─ 写入 runtime/chapter-XXXX.intent.md
  │     │
  │     └─ composeGovernedChapter()            — Phase 4 上下文组装
  │         ├─ collectSelectedContext()         — 收集所有上下文源
  │         │   ├─ chapter_memo（planner 产出）
  │         │   ├─ current_focus.md / author_intent.md
  │         │   ├─ outline/story_frame.md（语义选段）
  │         │   ├─ outline/volume_map.md（语义选段）
  │         │   ├─ 近5章摘要 trail + 章尾 trail
  │         │   ├─ 伏笔债务（memo 引用的 hook 原始种子）
  │         │   ├─ 记忆检索（facts / summaries / hooks / volume_summaries）
  │         │   └─ 同人正典（parent_canon / fanfic_canon）
  │         ├─ applyContextBudgetIfNeeded()     — 上下文预算控制
  │         │   └─ 超预算时：protected 保留 + compressible 语义压缩
  │         ├─ buildGovernedRuleStack()         — 构建治理规则栈
  │         ├─ buildGovernedTrace()             — 构建章节追踪
  │         └─ 写入 runtime/chapter-XXXX.context.json / rulestack.json / trace.json
  │
  ├─ 2. WriterAgent.writeChapter()             — 核心写作
  │     ├─ 读取全部 truth files（story_frame / volume_map / roles / state / hooks...）
  │     ├─ 加载近章正文（对话指纹提取）
  │     ├─ buildWriterSystemPrompt()           — 构建写手 prompt（含黄金开篇纪律）
  │     ├─ LLM 生成章节正文（流式）
  │     ├─ parseCreativeOutput()               — 解析标题+正文
  │     ├─ Settler 状态结算（Observer + Reflector）
  │     │   ├─ 观察章节正文 → 提取状态变更
  │     │   └─ 更新 current_state / pending_hooks / chapter_summaries /
  │     │       emotional_arcs / character_matrix
  │     └─ postWriteValidator()                — 写后校验（段落漂移/重复/AI味）
  │
  ├─ 3. Chapter Review Cycle（审查循环）
  │     │  根据 chapterReviewMode 分两种路径：
  │     │
  │     ├─ [manual 模式] 写完即停，跳过自动审查
  │     │   └─ 用户后续手动触发 review/revise
  │     │
  │     └─ [auto 模式]（默认）
  │         ├─ normalizeIfHardDrift()           — 字数硬偏离时归一化
  │         ├─ assess() — 首轮审计
  │         │   ├─ ContinuityAuditor.auditChapter()  — 33 维质量审查
  │         │   ├─ analyzeAITells()                   — AI 痕迹检测
  │         │   ├─ analyzeSensitiveWords()            — 敏感词检测
  │         │   └─ postWriteChecks()                  — 确定性写后检查
  │         │
  │         ├─ [未通过] 循环修复（默认1轮，可配置）
  │         │   ├─ ReviserAgent.reviseChapter(mode="auto")
  │         │   ├─ 重新 assess() 审计修订后内容
  │         │   ├─ 通过(≥85分) → 退出
  │         │   ├─ 净提升(≥3分) → 继续下一轮
  │         │   └─ 无净提升 → 退出，回退到最高分版本
  │         │
  │         └─ 最终选择所有快照中得分最高的版本
  │
  ├─ 4. Hook 伏笔晋升检查
  │     └─ rerunPromotionPass() — 基于 advanced_count 检查伏笔是否应升级
  │
  ├─ 5. 持久化
  │     ├─ resolveDuplicateTitle()              — 章节标题去重
  │     ├─ persistChapterArtifacts()            — 写入 chapters/XXXX.md + 更新索引
  │     ├─ analyzeLongSpanFatigue()             — 长跨度疲劳分析
  │     ├─ buildLengthTelemetry()               — 字数遥测
  │     │
  │     ├─ validateChapterTruthPersistence()    — 真相文件一致性校验
  │     │   └─ StateValidatorAgent 对比新旧 truth files
  │     │       ├─ 通过 → 确认写入
  │     │       └─ 不通过 → 进入状态降级模式（state-degraded）
  │     │
  │     ├─ saveRuntimeStateSnapshot()           — 保存运行时状态快照
  │     └─ 更新 chapter_summaries / pending_hooks / current_state 等文件
  │
  ├─ 6. dispatchNotification()                  — 通知（Telegram/飞书/企业微信/Webhook）
  │
  └─ 7. releaseLock()                           — 释放书籍写锁
```

**返回值** `ChapterPipelineResult`：
- `status`: `"ready-for-review"` | `"audit-failed"` | `"state-degraded"`
- `auditResult`: 审计结果（分数 + 问题列表）
- `revised`: 是否经过修订
- `tokenUsage`: Token 消耗统计

---

## 四、分支流程

### 4.1 建书骨架流程（Architect + Foundation Reviewer）

**源码**：`agents/architect.ts` / `agents/foundation-reviewer.ts`

```
ArchitectAgent.generateFoundation(book, externalContext, reviewFeedback)
  │
  ├─ 读取题材配置（genres/*.md）→ GenreProfile
  ├─ 构建系统 prompt（中文/英文双版本）
  │   ├─ 5 个 SECTION 输出契约
  │   ├─ 散文密度要求 + 预算限制
  │   ├─ 去重铁律（主角弧线只在 roles，世界铁律只在 story_frame...）
  │   └─ OKR 递归大纲法
  │
  ├─ LLM 调用（temperature: 0.8）
  │
  └─ parseSectionsWithRepair()
      ├─ parseSections() — 解析 === SECTION: === 块
      ├─ [缺失] repairMissingSections() — LLM 修复缺失段落
      └─ [仍缺失] 抛出 ArchitectIncompleteFoundationError（提示换更强模型）
```

**Architect 输出的 5 个 SECTION**：

| SECTION | 内容 | 预算 |
|---------|------|------|
| `story_frame` | 主题与基调 / 核心冲突(前台+后台) / 世界观底色 / 终局方向+全书Objective | ≤3000字 |
| `volume_map` | 各卷主题与情绪曲线 / 卷间钩子 / 各卷OKR(3个KR) / 卷尾不可逆改变 / 节奏原则(6条) | ≤5000字 |
| `roles` | 一人一卡（核心标签/反差细节/小传/主角弧线/当前现状/关系网络/内在驱动/成长弧光） | ≤8000字 |
| `book_rules` | 普通Markdown规则卡（主角/题材锁/叙事人称/数值规则/禁止事项） | ≤1000字 |
| `pending_hooks` | 初始伏笔池（13列Markdown表格，含依赖链/回收卷/核心标记/半衰期） | ≤2000字 |

**Foundation Reviewer 审查**：
- 多维度评分（总分 100，通过线 80，单维度下限 60）
- 支持三种模式：`original`（原创）/ `fanfic`（同人）/ `series`（系列续写）
- 未通过时将审核反馈注入 reviewFeedback，Architect 重新生成

---

### 4.2 章节规划流程（Planner）

**源码**：`agents/planner.ts` / `agents/planner-context.ts` / `agents/planner-prompts.ts`

```
PlannerAgent.planChapter(input)
  │
  ├─ loadPlanningSeedMaterials()
  │   ├─ readStoryFrame() / readVolumeMap()     — 大纲
  │   ├─ readCurrentFocus() / readAuthorIntent() — 控制文档
  │   ├─ readCurrentState() / readChapterSummaries()
  │   └─ previousEndingExcerpt                  — 上一章结尾摘录
  │
  ├─ findOutlineNode(volumeOutline, chapterNumber)
  │   └─ 在 volume_map 中定位当前章节对应的卷级节点
  │       ├─ 精确匹配（第N章 / Chapter N）
  │       ├─ 范围匹配（第N-M章 / Chapter N-M）
  │       └─ 锚点行回退
  │
  ├─ deriveGoal() — 推导章节目标（优先级链）
  │   ├─ 外部指令 → 局部覆盖 → 大纲节点 → 当前聚焦 → 作者意图 → 默认
  │
  ├─ readAuthoritativeBookRules() — 读取结构化规则
  ├─ collectMustKeep() / collectMustAvoid() / collectStyleEmphasis()
  │
  ├─ gatherPlanningMaterials() — 收集规划素材 + 记忆检索
  │   └─ retrieveMemorySelection() — 从 SQLite 记忆库检索 facts/summaries/hooks
  │
  ├─ ChapterIntentSchema.parse() — 构建确定性 ChapterIntent
  │   └─ { chapter, goal, outlineNode, arcContext, mustKeep, mustAvoid, styleEmphasis }
  │
  ├─ planChapterMemo() — LLM 生成 ChapterMemo（最多重试3次）
  │   ├─ 构建 user message（前章结尾/近3章摘要/角色矩阵行/伏笔线程/可回收钩子）
  │   ├─ 黄金开篇指导（中文前3章 / 英文前5章）
  │   ├─ LLM 调用（temperature: 0.7）
  │   ├─ parseMemo() — 解析 7 段 memo
  │   │   ├─ 本章目标 / 关联线索 / 当前任务 / 读者在等什么
  │   │   ├─ 该兑现的/暂不掀的 / 日常过渡承担什么 / 关键抉择三连问
  │   │   ├─ 章尾必须发生的改变 / 本章hook账 / 不要做
  │   └─ [失败] 追加错误反馈重试
  │   └─ [全部失败] 生成 fallback memo（降级但不崩溃）
  │
  └─ renderIntentMarkdown() — 渲染为 runtime/chapter-XXXX.intent.md
      └─ 含伏笔预算（活跃伏笔接近12条上限时警告）
```

**关键设计**：
- **稀疏 memo**：planner 只产出方向性指导，不写具体情节，留给 writer 发挥空间
- **黄金开篇纪律**：前几章（中文≤3 / 英文≤5）有专门的黄金开篇指导
- **伏笔预算**：活跃伏笔接近 12 条上限时，planner 会收到"优先回收旧债"的警告

---

### 4.3 上下文组装流程（Composer）

**源码**：`agents/composer.ts` / `utils/context-assembly.ts` / `utils/governed-context.ts`

```
composeGovernedChapter(input)
  │
  ├─ collectSelectedContext() — 收集所有上下文源
  │   │
  │   ├─ [1] chapter_memo — planner 产出的方向性 memo
  │   ├─ [2] current_focus.md — 当前聚焦（近期方向）
  │   ├─ [3] author_intent.md — 作者意图（长期方向，binding）
  │   ├─ [4] audit_drift.md — 上章审计漂移指导
  │   ├─ [5] current_state.md — 硬状态事实
  │   ├─ [6] outline/story_frame.md — 语义选段（LLM 选择相关段落）
  │   ├─ [7] outline/volume_map.md — 语义选段
  │   ├─ [8] parent_canon.md / fanfic_canon.md — 同人正典
  │   ├─ [9] 近5章摘要 trail — 标题/情绪/章尾（防重复）
  │   ├─ [10] 伏笔债务 — memo 引用的 hook 的原始种子文本
  │   ├─ [11] 记忆检索 — facts / summaries / hooks / volume_summaries
  │   │       └─ retrieveMemorySelection() 从 SQLite 检索
  │   └─ [12] 角色矩阵 / 情感弧线 / 伏笔快照
  │
  ├─ applyContextBudgetIfNeeded() — 上下文预算控制
  │   ├─ estimateTextTokens() — 估算总 token
  │   ├─ [未超预算] 直接使用
  │   └─ [超预算]
  │       ├─ 拆分 protected / compressible
  │       │   ├─ protected: author_intent / current_focus / 硬状态 / 活跃伏笔证据
  │       │   └─ compressible: 章节摘要 / 大纲选段 / 次要记忆
  │       ├─ [protected 超预算] 抛出错误（不压缩受保护内容）
  │       └─ [compressible 超预算] LLM 语义编译压缩
  │           └─ compileCompressibleContext() — 保留人名/承诺/证据/时间点
  │
  ├─ buildGovernedRuleStack() — 构建治理规则栈
  │   └─ 从 Intent 提取 mustKeep / mustAvoid / styleEmphasis
  │
  ├─ buildGovernedTrace() — 构建章节追踪
  │   └─ 记录 chapterNumber / plan / contextPackage / 使用的 skills / 压缩信息
  │
  └─ writeGovernedRuntimeArtifacts() — 落盘
      ├─ runtime/chapter-XXXX.context.json
      ├─ runtime/chapter-XXXX.rulestack.json
      └─ runtime/chapter-XXXX.trace.json
```

**关键设计**：
- **protected / compressible 分层**：受保护内容（作者意图、当前聚焦、硬状态、活跃伏笔）绝不压缩
- **语义选段**：大纲不是全量塞入，而是 LLM 根据章节目标选择相关段落
- **伏笔债务**：memo 引用的每个 hook 都附带原始种子文本，确保 writer 能正确推进

---

### 4.4 章节写作流程（Writer + Settler）

**源码**：`agents/writer.ts` / `agents/writer-prompts.ts` / `agents/settler-prompts.ts`

```
WriterAgent.writeChapter(input)
  │
  ├─ 1. 加载全部上下文
  │   ├─ story_frame / volume_map / roles / current_state / pending_hooks
  │   ├─ chapter_summaries / subplot_board / emotional_arcs / character_matrix
  │   ├─ style_guide / style_profile.json / parent_canon / fanfic_canon
  │   ├─ 近章正文（对话指纹提取，5章窗口）
  │   └─ 题材配置 + book_rules
  │
  ├─ 2. 构建写作 prompt
  │   ├─ buildWriterSystemPrompt()
  │   │   ├─ 角色设定 + 题材底色
  │   │   ├─ 上下文预算裁剪（各源有字符上限）
  │   │   ├─ POV 过滤（filterMatrixByPOV / filterHooksByPOV）
  │   │   ├─ 治理记忆证据块（governed memory evidence）
  │   │   ├─ 叙事意图简报（narrative intent brief）
  │   │   ├─ 黄金开篇纪律（前几章特殊指导）
  │   │   └─ 长跨度疲劳分析（防角色声音漂移）
  │   └─ 构建用户消息（chapter memo + context package）
  │
  ├─ 3. LLM 生成章节正文（流式，temperature 可覆盖）
  │   └─ parseCreativeOutput() — 解析标题 + 正文
  │
  ├─ 4. Settler 状态结算（双 Agent）
  │   ├─ Observer（观察者）
  │   │   ├─ buildObserverSystemPrompt() / buildObserverUserPrompt()
  │   │   └─ 观察章节正文 → 提取事件 / 状态变更 / 伏笔活动 / 情感变化
  │   │
  │   └─ Reflector（反射器/结算器）
  │       ├─ buildSettlerSystemPrompt() / buildSettlerUserPrompt()
  │       ├─ 基于 Observer 观察 → 生成 RuntimeStateDelta
  │       │   ├─ 更新 current_state.md
  │       │   ├─ 更新 pending_hooks.md（推进/回收/新建伏笔）
  │       │   ├─ 追加 chapter_summaries.md（本章摘要行）
  │       │   ├─ 更新 emotional_arcs.md
  │       │   └─ 更新 character_matrix.md
  │       └─ parseSettlerDeltaOutput() / parseSettlementOutput() — 解析增量
  │
  ├─ 5. 写后校验
  │   ├─ validatePostWrite() — 段落长度漂移 / 段落形状 / 重复标题 / 跨章重复
  │   ├─ detectCrossChapterRepetition() — 跨章重复检测
  │   ├─ analyzeAITells() — AI 痕迹检测
  │   ├─ analyzeHookHealth() — 伏笔健康分析
  │   └─ normalizePostWriteSurface() — 表面归一化
  │
  └─ 6. 返回 WriteChapterOutput
      └─ content / title / wordCount / runtimeStateDelta / postWriteErrors
```

**关键设计**：
- **治理上下文**：writer 接收的是 composer 组装好的 governed context，而非直接读文件
- **对话指纹**：从近5章提取角色对话指纹，保持角色声音一致性
- **双 Agent 结算**：Observer 先观察，Reflector 再结算，避免直接让 writer 自己更新状态
- **增量更新**：settler 输出 RuntimeStateDelta，通过 state-reducer 应用到状态

---

### 4.5 章节审查循环流程（Chapter Review Cycle）

**源码**：`pipeline/chapter-review-cycle.ts`

```
runChapterReviewCycle(params)
  │
  ├─ 1. 字数归一化（仅在硬偏离时触发）
  │   └─ normalizeIfHardDrift()
  │       └─ LengthNormalizerAgent — 字数超出硬范围时调整
  │
  ├─ 2. 首轮审计 assess()
  │   ├─ ContinuityAuditor.auditChapter()
  │   │   └─ 33 维质量审查（连续性/逻辑/人设/节奏/伏笔...）
  │   │       └─ 输出 AuditResult { passed, issues[], overallScore }
  │   ├─ analyzeAITells() — AI 痕迹（"不禁""仿佛"等标记词）
  │   ├─ analyzeSensitiveWords() — 敏感词（block/warn两级）
  │   ├─ runPostWriteChecks() — 确定性检查 + 伏笔账本校验
  │   └─ 综合评分 + lengthInRange 硬门
  │
  ├─ 3. 通过判定 isPassed()
  │   └─ passed && score >= 85 && lengthInRange
  │
  ├─ 4. [未通过] 循环修复（默认1轮，可配置 maxReviewIterations）
  │   └─ for iteration in 0..maxReviewIterations:
  │       ├─ ReviserAgent.reviseChapter(mode="auto")
  │       │   └─ 根据问题列表自动选择修复策略
  │       ├─ normalizePostWriteSurface() — 表面归一化
  │       ├─ 重新 assess() 审计修订后内容
  │       │
  │       ├─ [通过] → 退出循环
  │       ├─ [净提升 ≥3分] → 继续下一轮
  │       └─ [无净提升] → 退出循环
  │
  └─ 5. 选择最佳版本
      └─ snapshots.reduce() — 从所有快照中选得分最高 + 字数在范围内的版本
          └─ [修复反而变差] 回退到之前的最佳版本
```

**通过条件**（三重门）：
1. `auditResult.passed` — 无阻断性问题
2. `score >= 85` — 质量分数达标
3. `lengthInRange` — 字数在硬范围内

**退出策略**：
- 通过即退出
- 净提升不足（<3分）即退出
- 无新内容产出即退出
- 最终回退到最高分快照

---

### 4.6 回炉/重写流程（Reviser）

**源码**：`agents/reviser.ts`

Reviser 支持 **6 种修订模式**：

| 模式 | 说明 | 改动范围 |
|------|------|----------|
| `auto` | 自动模式（默认），根据问题严重度自动选择策略 | 按问题分级处理 |
| `polish` | 润色：只改表达、节奏、段落呼吸 | 用词/句序/标点，禁改事实剧情 |
| `rewrite` | 改写：重组问题段落，调整叙述力度 | 问题段落及直接上下文 |
| `rework` | 重写：重构场景推进和冲突组织 | 可大改，但不改主设定和大事件 |
| `anti-detect` | 反检测：降低 AI 可检测性 | 句式/口语化/情绪外化/段落差异化 |
| `spot-fix` | 定点修复：只改审稿指出的具体句子 | 问题句子±1句 |

```
ReviserAgent.reviseChapter(bookDir, content, chapterNumber, issues, mode, genre, options)
  │
  ├─ 加载上下文（state/hooks/outline/roles/summaries/规则）
  ├─ 构建问题列表（auto 模式按 critical/high/medium 分层）
  ├─ 根据 mode 构建系统 prompt
  │   ├─ [auto] buildAutoSystemPrompt() — 智能选择修复策略
  │   └─ [其他] 使用 MODE_DESCRIPTIONS[mode] 固定描述
  ├─ LLM 调用生成修订内容
  └─ 返回 ReviseOutput { revisedContent, fixedIssues, updatedState... }
```

**auto 模式的智能策略**：
- 输出模式：`patch-only` / `rewrite-only` / `allow-full`
- 根据问题严重度自动决定是局部修补还是整段重写

**anti-detect 反检测改写手法**（9 条）：
1. 打破句式规律
2. 口语化替代
3. 减少"了"字密度
4. 转折词降频
5. 情绪外化
6. 删叙述者结论
7. 群像反应具体化
8. 段落长度差异化
9. 消灭 AI 标记词

---

### 4.7 润色流程（Polisher）

**源码**：`agents/polisher.ts`

Polisher 是 Reviser 的轻量级补充，**仅改文笔表面**：

```
PolisherAgent
  │
  ├─ 只允许：替换用词 / 调整句序 / 修改标点节奏
  ├─ 禁止：增删段落 / 改人名地名 / 加新情节 / 改因果关系
  └─ 发现问题标记 [polisher-note]（不自行修改，留给后续处理）
```

与 Reviser 的区别：
- Polisher 不接收 issues 列表，是独立的文笔优化
- Polisher 发现剧情问题时只标记不修改
- Reviser 的 `polish` 模式更接近 Polisher，但由 issues 驱动

---

### 4.8 导入续写流程（Import Chapters）

**入口**：`PipelineRunner.importChapters(input)`
**源码**：`pipeline/runner.ts:2729`

```
importChapters({ bookId, chapters, resumeFrom, importMode })
  │
  ├─ 1. 构建导入资料包
  │   └─ buildImportFoundationSource(chapters, language)
  │       ├─ [小书 ≤80K字] 全量打包
  │       └─ [大书 >80K字] 压缩打包
  │           ├─ 章节标题目录（截断）
  │           ├─ 开头4章 + 结尾4章 + 中段8个锚点
  │           └─ 每章头尾摘录（6000字）
  │
  ├─ 2. ArchitectAgent.generateFoundationFromImport()
  │   ├─ 从已有正文反向推导基础设定
  │   ├─ importMode: "continuation"（续写） / "series"（系列新故事）
  │   │   ├─ continuation: 自然延续已有弧线
  │   │   └─ series: 引入新叙事空间，5章内引爆，50%+场景新鲜
  │   └─ 生成 5 段 SECTION（同建书流程）
  │
  ├─ 3. Foundation Reviewer 审查
  ├─ 4. 落盘基础设定文件
  │
  ├─ 5. 逐章回放（重建 truth files）
  │   └─ for chapter in chapters:
  │       ├─ WriterAgent.writeChapter() — 不生成新内容，用已有正文
  │       ├─ Settler 状态结算 — 从已有正文提取状态
  │       └─ 更新 current_state / pending_hooks / chapter_summaries
  │
  └─ 6. 返回 ImportChaptersResult
```

**关键设计**：
- 导入不是简单复制，而是**反向推导 + 逐章回放**
- 大书自动压缩资料包，避免超出 LLM 上下文
- 续写 vs 系列两种模式决定后续叙事空间

---

### 4.9 卷级合并流程（Consolidator）

**源码**：`agents/consolidator.ts`

```
ConsolidatorAgent.consolidate(bookDir)
  │
  ├─ 1. 读取 chapter_summaries.md + volume_map.md
  ├─ 2. parseVolumeBoundaries() — 解析卷边界
  ├─ 3. 识别已完成卷（所有章节都已写完）
  │
  ├─ 4. rerunAdvancedCountPromotion() — 伏笔晋升检查
  │   └─ 基于 advanced_count 判断种子伏笔是否应升级为活跃
  │
  ├─ 5. 对每个已完成卷
  │   ├─ LLM 压缩章节摘要为叙事段落（≤500字）
  │   └─ 保留关键人名/地点/情节点
  │
  ├─ 6. 归档旧摘要，保留当前卷的详细行
  │   ├─ 写入 volume_summaries.md（卷级叙事段落）
  │   └─ 更新 chapter_summaries.md（只留当前卷）
  │
  └─ 返回 ConsolidationResult { volumeSummaries, archivedVolumes, promotedHookCount }
```

**作用**：长书上下文压缩，防止越写越乱。已完成卷的逐章摘要压缩为卷级叙事段落，减少后续章节的上下文消耗。

---

### 4.10 短篇生产流程（Short Fiction Runner）

**源码**：`pipeline/short-fiction-runner.ts` / `agents/short-fiction.ts`

短篇有独立的流水线，不复用长篇的 PipelineRunner：

```
runShortFictionProduction(options)
  │
  ├─ 1. 大纲阶段（3 轮）
  │   ├─ ShortFictionOutlineAgent.createOutline()  → outline/v001.md
  │   ├─ ShortFictionOutlineReviewerAgent.reviewOutline() → reviews/outline-v001.md
  │   └─ ShortFictionOutlineReviserAgent.reviseOutline() → outline/v002.md
  │
  ├─ 2. 写作阶段
  │   ├─ ShortFictionWriterAgent.writeDraft()       → draft/v001.md
  │   │   └─ 一次性写完全部章节
  │   │   └─ [有缺失章节] continueDraft() 补写（最多3次重试）
  │   │
  │   ├─ ShortFictionDraftReviewerAgent.reviewDraft() → reviews/draft-v001.md
  │   │
  │   └─ ShortFictionDraftReviserAgent.reviseDraft()  → draft/v002.md
  │       └─ [改稿失败/不完整] 保留 v001，记录警告
  │
  ├─ 3. 最终产物
  │   ├─ writeFinalArtifacts() → final/full.md + final/full.json
  │   └─ ShortFictionPackagingAgent.generatePackage()
  │       └─ 生成卖点包装（标题/简介/卖点/封面prompt）→ sales-package.md
  │
  └─ 4. 封面生成（可选）
      └─ generateCoverArtifact() → cover.png
```

**与长篇的区别**：
- 独立的大纲→审查→修订流程（不使用 Architect）
- 一次性写完全部章节（非逐章循环）
- 自带卖点包装和封面生成
- 支持 storyId 断点续跑（已完成的不会重做）

---

### 4.11 自动写作/定时调度流程（Scheduler）

**源码**：`pipeline/scheduler.ts`

```
Scheduler.start()
  │
  ├─ 立即触发一次 runWriteCycle()
  │
  ├─ 定时任务1：write-cycle（按 writeCron 间隔）
  │   └─ runWriteCycle()
  │       ├─ 遍历所有活跃书籍
  │       ├─ 检查质量门（qualityGates）
  │       │   ├─ consecutiveFailures — 连续失败次数
  │       │   ├─ failureDimensions — 失败维度聚类
  │       │   ├─ dailyChapterCount — 日章数上限
  │       │   └─ pausedBooks — 暂停的书
  │       ├─ pipeline.writeNextChapter(bookId)
  │       └─ [可选] detectAndRewrite() — AIGC 检测+自动重写
  │
  └─ 定时任务2：radar-scan（按 radarCron 间隔）
      └─ RadarAgent 扫榜（番茄/起点等平台热门数据）
```

**质量门机制**：
- 连续失败超阈值 → 暂停该书
- 失败维度聚类 → 识别系统性问题
- 日章数上限 → 防止失控
- 冷却时间 → 章节间冷却

---

### 4.12 状态校验与修复流程

**源码**：`agents/state-validator.ts` / `pipeline/chapter-truth-validation.ts` / `pipeline/chapter-state-recovery.ts`

```
状态校验（每章写完后触发）
  │
  ├─ validateChapterTruthPersistence()
  │   └─ StateValidatorAgent 对比新旧 truth files
  │       ├─ current_state.md — 当前状态是否被矛盾修改
  │       ├─ pending_hooks.md — 伏笔是否被错误回收/丢失
  │       ├─ story_frame.md — 世界观铁律是否被违背
  │       └─ book_rules.md — 规则是否被违反
  │
  ├─ [校验通过] → 确认写入，status = "ready-for-review"
  │
  └─ [校验失败] → 状态降级
      ├─ status = "state-degraded"
      ├─ parseStateDegradedReviewNote() — 解析降级原因
      └─ 标记需要 repairChapterState 修复

状态修复（手动触发或自动触发）
  │
  ├─ repairChapterState(bookId, chapterNumber)
  │   ├─ retrySettlementAfterValidationFailure()
  │   │   └─ 重新调用 Settler 结算，注入 validationFeedback
  │   └─ [成功] 恢复正常状态
  │
  └─ resyncChapterArtifacts(bookId, chapterNumber)
      └─ 重新同步章节产物（章节文件/摘要/状态）
```

---

### 4.13 AIGC 检测与反检测流程

**源码**：`pipeline/detection-runner.ts` / `agents/detector.ts` / `agents/ai-tells.ts`

```
AIGC 检测（可在写章后或手动触发）
  │
  ├─ detectChapter(content)
  │   ├─ DetectorAgent — AIGC 检测
  │   └─ analyzeAITells() — AI 痕迹分析
  │       └─ 检测"不禁""仿佛""宛如"等 AI 标记词
  │
  └─ detectAndRewrite(bookId, chapterNumber)
      ├─ 检测 AI 比例超阈值
      └─ [超阈值] 自动触发 ReviserAgent(mode="anti-detect")
          └─ 反检测改写（9种手法）
```

**检测维度**：
- AI 痕迹词频
- 句式规律性
- 段落等长性
- 叙述者结论密度

---

### 4.14 基础设定修订流程（reviseFoundation）

**入口**：`PipelineRunner.reviseFoundation(bookId, feedback)`
**源码**：`pipeline/runner.ts:790`

```
reviseFoundation(bookId, feedback)
  │
  ├─ 1. 备份现有基础设定
  │   ├─ Phase5 书：备份 outline/ + roles/
  │   └─ Phase4 书：备份 story_bible/volume_outline/book_rules/character_matrix
  │
  ├─ 2. 读取现有设定（Phase5 读 outline/ + roles/，Phase4 读扁平文件）
  │
  ├─ 3. ArchitectAgent.generateFoundation(reviseFrom: { 旧设定, userFeedback })
  │   └─ 将旧设定重组为当前 5 段 SECTION 格式
  │       ├─ 保留原有世界观/角色/主线/伏笔/语气
  │       ├─ 不改动已写章节的运行时事实
  │       └─ 不重置 current_state / pending_hooks 之外的运行时日志
  │
  ├─ 4. Foundation Reviewer 审查（失败仅警告，不阻断）
  │
  └─ 5. writeFoundationFiles(mode="revise")
      └─ 清空旧 roles/ 目录后重新写入
```

**适用场景**：已有书的架构稿从旧格式升级，或按用户反馈二次重写基础设定。

---

## 五、核心数据模型与文件结构

### 5.1 书籍目录结构

```
books/<book-id>/
├── book.json                          — 书籍配置（BookConfig）
├── chapters/
│   ├── 0001.md                        — 章节正文（前缀章号）
│   ├── 0002.md
│   └── ...
├── chapter-index.json                 — 章节索引
└── story/
    ├── outline/
    │   ├── story_frame.md             — 主题/冲突/世界观/终局
    │   ├── volume_map.md              — 分卷地图+节奏原则
    │   └── rhythm_principles.md       — 节奏原则（仅legacy有独立文件）
    ├── roles/
    │   ├── 主要角色/<name>.md          — 主角弧线权威来源
    │   └── 次要角色/<name>.md
    ├── runtime/
    │   ├── chapter-0001.intent.md     — 章节意图
    │   ├── chapter-0001.context.json  — 上下文包
    │   ├── chapter-0001.rulestack.json — 规则栈
    │   └── chapter-0001.trace.json    — 章节追踪
    ├── author_intent.md               — 作者意图（长期方向）
    ├── current_focus.md               — 当前聚焦（近期方向）
    ├── current_state.md               — 当前状态（运行时追加）
    ├── pending_hooks.md               — 伏笔池（13列表格）
    ├── chapter_summaries.md           — 章节摘要（逐章行）
    ├── volume_summaries.md            — 卷级摘要（合并后）
    ├── emotional_arcs.md              — 情感弧线
    ├── character_matrix.md            — 角色矩阵（兼容指针）
    ├── book_rules.md                  — 规则卡
    ├── story_bible.md                 — 故事圣经（兼容指针）
    ├── particle_ledger.md             — 粒子账本（运行时日志）
    ├── subplot_board.md               — 支线看板
    ├── style_guide.md                 — 文风指南
    ├── style_profile.json             — 文风指纹
    ├── parent_canon.md                — 正传正典（同人/番外用）
    ├── fanfic_canon.md                — 同人正典
    ├── memory.db                      — SQLite 记忆数据库
    ├── snapshots/                     — 状态快照
    └── state/                         — 结构化状态
```

### 5.2 核心数据模型

| 模型 | 文件 | 说明 |
|------|------|------|
| `BookConfig` | `models/book.ts` | 书籍配置（平台/题材/目标章数/每章字数/审查模式/修订门） |
| `ChapterIntent` | `models/input-governance.ts` | 确定性章节意图（goal/outlineNode/mustKeep/mustAvoid） |
| `ChapterMemo` | `models/input-governance.ts` | LLM 生成的 7 段章节 memo |
| `ContextPackage` | `models/input-governance.ts` | 上下文包（selectedContext[]） |
| `RuleStack` | `models/input-governance.ts` | 治理规则栈 |
| `ChapterTrace` | `models/input-governance.ts` | 章节追踪（记录上下文来源/压缩/skills） |
| `RuntimeStateDelta` | `models/runtime-state.ts` | 运行时状态增量 |
| `HookRecord` | `models/runtime-state.ts` | 伏笔记录（13列） |
| `AuditResult` | `agents/continuity.ts` | 审计结果（passed/issues[]/overallScore） |
| `LengthSpec` | `models/length-governance.ts` | 字数规格（target/min/max/countingMode） |

---

## 六、关键源码文件索引

### 核心流水线
| 文件 | 作用 |
|------|------|
| `pipeline/runner.ts` | **PipelineRunner** — 写作流水线核心编排器（1700+行） |
| `pipeline/chapter-review-cycle.ts` | 章节审查循环（审计→修订→重审） |
| `pipeline/chapter-persistence.ts` | 章节持久化 |
| `pipeline/chapter-truth-validation.ts` | 章节真相一致性校验 |
| `pipeline/chapter-state-recovery.ts` | 章节状态恢复 |
| `pipeline/scheduler.ts` | 定时调度器（cron 驱动自动写作） |
| `pipeline/short-fiction-runner.ts` | 短篇生产流水线 |
| `pipeline/detection-runner.ts` | AIGC 检测+自动重写 |
| `pipeline/persisted-governed-plan.ts` | 持久化治理计划 |

### Agent 层
| 文件 | 作用 |
|------|------|
| `agents/base.ts` | BaseAgent 基类 |
| `agents/architect.ts` | 架构师（建书骨架） |
| `agents/foundation-reviewer.ts` | 基础设定审查员 |
| `agents/planner.ts` | 章节规划器 |
| `agents/composer.ts` | 上下文组装器 |
| `agents/writer.ts` | 章节写手 |
| `agents/continuity.ts` | 连续性审查员（33维） |
| `agents/reviser.ts` | 修订器（6种模式） |
| `agents/polisher.ts` | 润色器 |
| `agents/consolidator.ts` | 卷级合并器 |
| `agents/state-validator.ts` | 状态校验器 |
| `agents/post-write-validator.ts` | 写后校验器 |
| `agents/ai-tells.ts` | AI 痕迹检测 |
| `agents/sensitive-words.ts` | 敏感词检测 |
| `agents/length-normalizer.ts` | 字数归一化器 |
| `agents/short-fiction.ts` | 短篇全套 Agent |

### 状态管理
| 文件 | 作用 |
|------|------|
| `state/manager.ts` | StateManager（书籍锁/配置/truth files） |
| `state/memory-db.ts` | SQLite 记忆数据库 |
| `state/runtime-state-store.ts` | 运行时状态快照存储 |
| `state/state-reducer.ts` | 状态 reducer（应用增量） |
| `state/state-bootstrap.ts` | 状态引导（从 Markdown 重建） |

### 伏笔系统
| 文件 | 作用 |
|------|------|
| `utils/hook-governance.ts` | 伏笔治理（准入/分类/陈旧） |
| `utils/hook-promotion.ts` | 伏笔晋升 |
| `utils/hook-health.ts` | 伏笔健康分析 |
| `utils/hook-arbiter.ts` | 伏笔仲裁 |
| `utils/hook-stale-detection.ts` | 伏笔陈旧检测 |

### 交互层
| 文件 | 作用 |
|------|------|
| `interaction/runtime.ts` | 交互运行时（统一 action surface） |
| `interaction/request-router.ts` | 请求路由 |
| `interaction/action-envelope.ts` | Action 类型定义 |
| `interaction/edit-controller.ts` | 编辑控制器 |

### LLM 层
| 文件 | 作用 |
|------|------|
| `llm/provider.ts` | LLM 客户端 |
| `llm/providers/endpoints/` | 40+ 个服务端点 |
| `llm/secrets.ts` | 密钥管理 |

---

> **文档说明**：本文档基于 inkos 项目源码分析，覆盖了所有与"写小说"相关的流程。短篇/长篇/同人/导入续写均有独立分支流程，但核心写作链路（Planner→Composer→Writer→Audit→Revise→Settle→Persist）是共享的。
