# Roads: an app's branches as caminos

The **Roads** tab of each app tells the last two weeks of its builds as a story. The default branch is the *camino real*, a dirt road running left to right from oldest to newest. Every other branch is a *vereda*, a path that leaves the camino real at the commit it started from and, once merged, comes back to it. Each run is a stop along the way.

## What you see {#what-you-see}

| On the map | What it is |
|---|---|
| The **camino real** | the default branch; the stone markers (*mojoneras*) are days |
| A **vereda** | a branch. It leaves the camino real where it started (the merge base) and rejoins it where its pull request was merged. A stacked pull request, whose base is another branch, leaves and rejoins that branch instead |
| A whole **huacal** of maize | a run that passed |
| A broken huacal, maize spilled | a run that failed; the panel says at which step (`platform: tests`, `web: image`…) |
| A grey huacal | a cancelled run |
| A **market stall** on the camino real | a release that was deployed, with its number |
| A vereda that ends at a **sign** | a branch that was not merged: *waiting at the gate* while it ran in the last 48 hours or its pull request is open, *left on the way* after that |
| A dashed vereda | not merged (yet) |

Quiet stretches of time are shortened, so a busy afternoon gets more room than an empty weekend.

Tap a stop or a vereda to see its story in the panel: when it left and from where, its trips, how many times it stumbled, how long it took from leaving to merging, and a link to each run. With nothing selected, the panel sums up the two weeks: how many trips arrived whole, the typical life of a branch, how many branches passed on their first trip, the branches not merged, and the latest stumbles.

## Telling the story {#telling-the-story}

**Tell the story** walks the two weeks: a burro with its load goes down every vereda that is on its way, stumbles (dust, spilled maize) where a run failed, every merge rings the camino real, and the caravan with its *arriero* reaches the market with confetti (or fireflies at night) at each release. Choose the pace (*stroll*, 3 minutes; *trot*; *gallop*, 45 seconds), pause it, or tap the strip under the map to jump to a date.

Drag to travel the road; zoom with the **+ / −** buttons or Ctrl and the wheel. The map keeps its shape at any zoom. It follows the day or night theme of the top bar, and holds still when motion is off.

## Where the data comes from {#where-the-data-comes-from}

- **Runs** come from rendimiento's own database: every run of the last 14 days, with the steps that failed and the release it made.
- **Branches** come from GitHub, through the app's installation: the 100 most recently updated pull requests (merged or not, their base branch) and, for each branch, the commit it started from. For a merged branch that is found against the base just before the merge (the first parent of its merge or squash commit), since afterwards the base already contains it.
- What GitHub said about commits never changes, so it is remembered; the whole history is rebuilt when a newer run appears, or after two minutes. At most 60 branches, the most recently active, are looked up.
- If GitHub cannot answer, the tab still draws what it knows and says some branches may lack their pull request or starting point.

## In the code {#in-the-code}

| What | Where |
|---|---|
| Runs with their failed steps and release | `store.RunHistory` |
| Branches, forks and merges from the runs and GitHub | `internal/caminos` (`Build`) |
| GitHub calls and the cache | `platform/caminos.go` (`Platform.Caminos`), `github` (`RecentPullRequests`, `MergeBase`, `FirstParent`) |
| The API | `GET /api/apps/{app}/caminos` |
| The canvas and the panel | `web/src/components/caminos.ts`, `caminos-tab.tsx` |
