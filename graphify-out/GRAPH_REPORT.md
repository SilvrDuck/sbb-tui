# Graph Report - /home/thibault/code/sbb-tui  (2026-05-22)

## Corpus Check
- Corpus is ~13,312 words - fits in a single context window. You may not need a graph.

## Summary
- 260 nodes · 404 edges · 29 communities (15 shown, 14 thin omitted)
- Extraction: 94% EXTRACTED · 6% INFERRED · 0% AMBIGUOUS · INFERRED: 25 edges (avg confidence: 0.83)
- Token cost: 115,624 input · 28,906 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Logo & Tagline Rendering|Logo & Tagline Rendering]]
- [[_COMMUNITY_Layout & Result Views|Layout & Result Views]]
- [[_COMMUNITY_Connection Rendering Helpers|Connection Rendering Helpers]]
- [[_COMMUNITY_Animation Ticker|Animation Ticker]]
- [[_COMMUNITY_Release & Project Docs|Release & Project Docs]]
- [[_COMMUNITY_Input Update & Suggestions|Input Update & Suggestions]]
- [[_COMMUNITY_Config & Theme Loading|Config & Theme Loading]]
- [[_COMMUNITY_Transport API Client|Transport API Client]]
- [[_COMMUNITY_Model Init & Styles|Model Init & Styles]]
- [[_COMMUNITY_Connection Data Model|Connection Data Model]]
- [[_COMMUNITY_Bubbletea Messages|Bubbletea Messages]]
- [[_COMMUNITY_Semantic-Release Plugins|Semantic-Release Plugins]]
- [[_COMMUNITY_Shine Color Palette|Shine Color Palette]]
- [[_COMMUNITY_main.version|main.version]]
- [[_COMMUNITY_animationTickMsg type|animationTickMsg type]]
- [[_COMMUNITY_dataMsg type|dataMsg type]]
- [[_COMMUNITY_suggestionsMsg type|suggestionsMsg type]]
- [[_COMMUNITY_suggestTickMsg type|suggestTickMsg type]]
- [[_COMMUNITY_versionCheckMsg type|versionCheckMsg type]]
- [[_COMMUNITY_fadeOpts type|fadeOpts type]]
- [[_COMMUNITY_shineRestartMsg type|shineRestartMsg type]]
- [[_COMMUNITY_shineOpts type|shineOpts type]]
- [[_COMMUNITY_Start-Screen Anim Chain|Start-Screen Anim Chain]]
- [[_COMMUNITY_Timestamp.UnmarshalJSON|Timestamp.UnmarshalJSON]]
- [[_COMMUNITY_Timestamp.Sub|Timestamp.Sub]]

## God Nodes (most connected - your core abstractions)
1. `appModel` - 39 edges
2. `appModel.Update` - 14 edges
3. `appModel.updateInputs` - 10 edges
4. `applyShine()` - 9 edges
5. `animator` - 8 edges
6. `appModel.renderStartScreen` - 8 edges
7. `LoadConfig()` - 7 edges
8. `renderLink()` - 7 edges
9. `textBounds()` - 7 edges
10. `appModel.renderSimpleConnection` - 7 edges

## Surprising Connections (you probably didn't know these)
- `Partial theme override merge rationale` --rationale_for--> `mergeTheme()`  [EXTRACTED]
  CLAUDE.md → config/config.go
- `.goreleaser.yaml build/release config` --conceptually_related_to--> `NewerVersion()`  [INFERRED]
  .goreleaser.yaml → util/version.go
- `docs/themes.md theme presets` --shares_data_with--> `Theme`  [INFERRED]
  docs/themes.md → config/config.go
- `.releaserc.json semantic-release config` --references--> `Conventional Commits`  [INFERRED]
  .releaserc.json → CONTRIBUTING.md
- `main()` --calls--> `NewModel()`  [INFERRED]
  main.go → ui/model.go

## Hyperedges (group relationships)
- **Bubbletea tea.Model implementation trio** — ui_model_init, ui_update_update, ui_view_view [EXTRACTED 1.00]
- **Start-screen logo build/tagline build/shine cycle chain** — ui_logo_build_renderlogobuild, ui_tagline_build_rendertaglinebuild, ui_shine_startshinecycle, ui_shine_onanimationsfinished, ui_shine_shinerestartcmd [INFERRED 0.95]
- **Shine pass pipeline: band sweep -> per-cell intensity -> palette render** — ui_shine_applyshine, ui_shine_shinefactor, ui_shine_newshinepalette, ui_shine_rendergrid [EXTRACTED 1.00]
- **Automated Release Pipeline** — workflows_release_yml, releaserc_semantic_release_config, goreleaser_yaml_release_config, changelog_md, conventional_commits [EXTRACTED 0.95]
- **Pre-commit code quality gate** — pre_commit_config_yaml_hooks, golangci_yml_lint_config, conventional_commits [EXTRACTED 0.95]
- **Theme configuration & merge flow** — config_config_loadconfig, config_config_defaulttheme, config_config_mergetheme, config_config_theme, docs_themes_md [EXTRACTED 0.90]

## Communities (29 total, 14 thin omitted)

### Community 0 - "Logo & Tagline Rendering"
Cohesion: 0.1
Nodes (30): animator.Progress, animator.Registered, fadeOpts, appModel.renderLogoBuild, palette, paletteCell, sbb-logo.txt (ASCII logo asset), sbb-logo-nerdfont.txt (Nerd Font logo asset) (+22 more)

### Community 1 - "Layout & Result Views"
Cohesion: 0.1
Nodes (5): appModel, formatDuration(), googleMapsURL(), renderLink(), appModel.renderWalkSection

### Community 2 - "Connection Rendering Helpers"
Cohesion: 0.09
Nodes (27): animation, animator, animator.Elapsed, appModel.buildDetailLines, appModel.formatDelay, appModel.formatStationLine, appModel.maxDetailScroll, appModel.renderFullConnection (+19 more)

### Community 3 - "Animation Ticker"
Cohesion: 0.1
Nodes (19): animation, animationTickCmd(), newAnimator(), animator.Start, animator.StartIndefinite, animator.Stop, animator.Tick, animationTickMsg (+11 more)

### Community 4 - "Release & Project Docs"
Cohesion: 0.11
Nodes (19): Automated release pipeline rationale, Conventional Commits, dev version skips update check rationale, Europe/Zurich timezone, GitHub Releases API, .golangci.yml lint config, .goreleaser.yaml build/release config, SwissLocation (+11 more)

### Community 5 - "Input Update & Suggestions"
Cohesion: 0.25
Nodes (16): adaptSuggestions(), completeDate(), completeTime(), countDigitsBefore(), fetchSuggestionsCmd(), foldRune(), formatDate(), formatTime() (+8 more)

### Community 6 - "Config & Theme Loading"
Cohesion: 0.15
Nodes (17): Config, Config, configFilePath(), DefaultTheme(), fileConfig, LoadConfig(), loadFile(), mergeTheme() (+9 more)

### Community 7 - "Transport API Client"
Cohesion: 0.19
Nodes (14): connectionsResponse, FetchConnections(), FetchLocations(), locationsResponse, connectionsResponse, locationsResponse, Arrival, Connection (+6 more)

### Community 8 - "Model Init & Styles"
Cohesion: 0.21
Nodes (10): main, newIconSet(), iconSet, NewModel(), styles, detectBackground(), mustHex(), newStyles() (+2 more)

### Community 9 - "Connection Data Model"
Cohesion: 0.18
Nodes (7): Arrival, Connection, Coordinate, Departure, Section, Station, Timestamp

### Community 10 - "Bubbletea Messages"
Cohesion: 0.2
Nodes (9): dataMsg, focusable, capitalise(), checkVersionCmd(), appModel.Init, userError(), suggestionsMsg, suggestTickMsg (+1 more)

## Knowledge Gaps
- **68 isolated node(s):** `branches`, `plugins`, `locationsResponse`, `connectionsResponse`, `Config` (+63 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **14 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `appModel` connect `Layout & Result Views` to `Logo & Tagline Rendering`, `Bubbletea Messages`, `Input Update & Suggestions`?**
  _High betweenness centrality (0.368) - this node is a cross-community bridge._
- **Why does `NewModel()` connect `Model Init & Styles` to `Bubbletea Messages`, `Animation Ticker`, `Config & Theme Loading`?**
  _High betweenness centrality (0.157) - this node is a cross-community bridge._
- **Why does `checkVersionCmd()` connect `Bubbletea Messages` to `Release & Project Docs`?**
  _High betweenness centrality (0.112) - this node is a cross-community bridge._
- **What connects `branches`, `plugins`, `locationsResponse` to the rest of the system?**
  _68 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Logo & Tagline Rendering` be split into smaller, more focused modules?**
  _Cohesion score 0.1 - nodes in this community are weakly interconnected._
- **Should `Layout & Result Views` be split into smaller, more focused modules?**
  _Cohesion score 0.1 - nodes in this community are weakly interconnected._
- **Should `Connection Rendering Helpers` be split into smaller, more focused modules?**
  _Cohesion score 0.09 - nodes in this community are weakly interconnected._