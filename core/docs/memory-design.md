# Eriphe's memory: design notes

What the memory should become so that Eriphe can take useful initiatives, what we decided and why.
It follows the study "Mémoire intelligente pour la proactivité" (adopted 2026-11-01, option A: all
in SQLite on the user's machine, a cheap cloud model only at night) and a second round of reading
of the primary sources (papers' full text, projects' code), 2026-11-01. The user-facing reference
stays [README.md § Memory](../README.md#memory); this file keeps the reasons.

## Constraints

- **Light.** eriphe-core runs on a NAS without AVX (Pentium N6005) or a Raspberry Pi. The core
  memory adds a few MB of RAM and milliseconds of CPU per search; anything heavier (embeddings,
  a local model) is an optional tier, enabled only where the evaluation shows it pays.
- **No model per message.** The cloud model runs the nightly extraction (and a weekly
  consolidation, later). Everything that runs per turn or per minute is code.
- **Trust is code.** The taint rules of [README § Memory](../README.md#memory) hold for every new
  path: what was read outside never becomes believed, nor able to act, without the user.
- **Safe on live data.** Migrations are forward-only, add columns and tables, never drop user data,
  and are tested on a database written by the previous schema.

## What the primary sources say (and what we take)

| Source | Finding | What we take |
|---|---|---|
| LongMemEval (Wu et al., ICLR 2025, §5.3, App. E.2 Table 9) | Indexing by turn beats by session for answering; **expanding the key with extracted facts** raises BM25 round-level R@5 from 0.472 to 0.554 (+8.2 pts); keyphrases or summaries do **not** raise BM25 recall. Their BM25 is whitespace-split, no stemming. | The index key is the memory's text plus what the extraction adds. Synonyms are kept as a bridge for paraphrases (measured on our corpus, not assumed). |
| LongMemEval §5.4, `temp_query_search_pruning.py` | Time-aware query expansion: an LLM turns the question into a date range, widened by ±2 days, and matching sessions are **moved first, not filtered**; +6.8 to +11.3 % recall on temporal questions, but an 8B model "often mistakenly extracts a time range". | Code, not a model, reads only explicit time expressions (hier, en septembre, la semaine dernière, a date); the range reorders, never filters. |
| LoCoMo (Maharana et al., 2024, Table 3) | With DRAGON, retrieving dialogue turns: R@5 58.8; observations (extracted facts): 49.6; but answers are better from observations (F1 41.4 vs 31.7). Category numbers in the data are 1 multi-hop, 2 temporal, 3 open-domain, 4 single-hop, 5 adversarial (not the paper's order). | Extracted facts are the main unit; the raw conversation stays the audit trail. |
| LoCoMo audit (Penfield Labs, April 2026) | 6.4 % of reference answers wrong; an LLM judge accepts up to 63 % of deliberately wrong answers. | Our evaluation scores **exact keys**, never a model judge. |
| Generative Agents (Park et al., 2023, §4.1) | score = recency + importance + relevance, each min-max scaled to [0, 1], weights 1; recency decays 0.995 per hour; importance 1–10 rated at write time. | Additive, bounded combination: an old memory that matches well is not crushed by its age (ours multiplied, see below). Importance is rated by the extraction. |
| Graphify (ARCHITECTURE.md, extraction spec) | Every derived edge is EXTRACTED (stated), INFERRED (a deduction, discrete confidence 0.95…0.55) or AMBIGUOUS (flagged for review); discrete rubrics because "models follow discrete rubrics better than continuous ranges". | A `basis` on every derived memory (stated / inferred / ambiguous), beside its trust; discrete importance. |
| Zep / Graphiti (`edges.py`, `dedupe_edges.py`) | Bi-temporal edges (`valid_at`, `invalid_at`, `expired_at`); "soft deadlines are not invalid_at"; "do not infer contradiction from recency alone". | We already keep valid_from / valid_to / recorded_at and a log: same model without a graph database. Their two rules go into the extraction prompt. |
| Mem0 (v2.2, `configs/prompts.py`, `main.py`) | Now ADD-only with links to related memories; relative dates are made absolute against the **observation date**, "your only temporal anchor". | Relative dates are resolved against the segment's own date, never the run's. |
| Mastra Observational Memory (`packages/memory`) | Best published LongMemEval score (94.9 % gpt-5-mini): an observer every 30 k tokens and a reflector at 40 k keep a dated, prioritised log always in context (three dates: observed, message time, referenced). | Too heavy for a 2.5 k-token pack on a self-hosted model, and it needs a model per 30 k tokens. We take the three-date anchoring and the priority (importance) instead. |
| OpenClaw dreaming (docs) | Untrusted provenance is removed before consolidation; recalled context is stripped so it is not learnt back; a rewrite may not lose more than 25 % of entries; backup first. | Reading an external memory taints the turn, and what the brain says then is external for the extraction (PR 2). The guards are kept for the weekly consolidation (later). |
| Typed Intention Stores (Zhao & Wu, arXiv 2609.01272, §3) | Prospective memory is "schema-constrained state tracking": intentions are typed rows (trigger, action, status pending/done/canceled), **Filter and the commit are code**, the model only Forms rows from text and Revises them with typed patches (reschedule, override, cancel). PM-Bench Set-F1: DeepSeek 67.7 → 82.9; Gemma-E2B 4.2 → 66.2; every retrospective memory (Mem0, A-Mem, Letta…) is *below* the no-store baseline. Update handling stays the main residual. | Intentions are a table with a lifecycle in code (PR 5). The extraction Forms and Revises; a minute timer does Filter and Decide for time and person cues. |
| PM-Bench (Liu & Gabriel, arXiv 2607.12385) | A todo ledger has the best precision (73.2 %); automatic heartbeats every 30–60 min multiply false alarms (422–489 vs 134). | The brain is woken only when an intention *becomes* due, never on a periodic heartbeat. |
| Stemming (Savoy, SAC 2006; McNamee et al., SIGIR 2008, CLEF) | French: stemming adds +27 to +35 % MAP over no stemming with Okapi; Snowball beats character 4-grams for Romance languages (MAP 0.4558 vs 0.4442 vs 0.4267 for words). | Snowball French, in a shadow column of the full-text index; not the trigram tokenizer. |

## Decisions

### 1. Evaluate first, with keys, on our own French corpus (PR 1)

`testdata/memoryeval/camille.json`: a fictional freelancer over six weeks (a move, a new client, a
rate change, a laptop replaced, promises, waits, decisions, project progress). 130 items:

| Family | Items | Run | Score |
|---|---|---|---|
| extraction, multi-session, temporal, update, decision, progress, abstention | 85 questions | CI (retrieval), opt-in (answers) | recall@k of the memories that answer; with a model, exact answer keys |
| prospective | 25 checkpoints | CI, fake clock | Set-F1 of the due set (PM-Bench's metric) |
| interrupt or not | 20 labelled situations | CI, reported | accuracy and false interruptions (scored once the interrupt score exists) |

Plus 13 injection cases that must all hold (`memory_eval_injection_test.go`). A case the memory
does not hold yet is listed as a known gap: it is expected to fail, and the test fails when it
starts to hold, so the list stays true.

Retrieval is measured two ways: **raw** (the question's words only: what code alone finds) and
**tool** (with the filters a good brain passes to `recall`: as_of, since/until). Floors keep each
family from regressing; they are raised as the memory improves.

What needs a model is behind the `memoryeval` build tag, never in CI: the real nightly extraction
replayed over the six weeks, and answers to the questions from what `recall` returns, both scored
by exact keys. Public benchmarks are adapted the same way, retrieval only, no model:
LoCoMo (turns, and the observations it ships) and LongMemEval_S (turn and session recall).

### 2. Reading back is reading (PR 2)

The study found that a task's journal episode (trust `external`, active) could come back through
`recall`, the context pack or the wake packet into a turn judged clean. Fixed by one rule: a turn
that reads an external memory is tainted (`memory.external`); the packets nobody asked for name
such a memory without quoting it; and what the brain writes in a tainted turn is external for the
next extraction (otherwise a mail repeated by the brain would come back first-party). Making
external episodes "proposed until approved" was rejected: every finished task writes one, and an
approval queue that fills every day gets approved blindly.

### 3. Retrieval that finds what was said in other words (PR 3)

- **French stemming in a shadow column.** `memories` gets a `stems` column (Snowball French over
  the text, subject and keys, in pure Go: `github.com/kljensen/snowball`, MIT, no dependencies),
  indexed by FTS5 beside the text. A query matches the words as typed (with a prefix for long ones)
  or their stems. Snowball leaves the plural of short words ("vélos"), so a final *s* goes too, as
  in Savoy's minimal stemmer. Measured on our corpus (plain BM25, the question's words, text only,
  recall@5): words 68.9 %, words with prefixes 70.2 %, trigram tokenizer 70.2 % (74.3 % with each
  word cut to five letters), **words with prefixes and stems 75.7 %**; the index is 4.1 times the
  words' with trigrams, 1.8 times with the stems column. Trigram also finds nothing under three
  characters, and the CLEF evidence favours a stemmer for Romance languages.
- **The key is the memory plus what the extraction adds**: `keywords` (variants and synonyms that
  bridge paraphrases: voiture, bagnole, véhicule), indexed with a lower weight.
- **Stop words** (Snowball's French and English lists and the question words) are left out of
  queries: "quel est le nom de" matched everything; so is the user's name, which is in nearly every
  memory about them.
- **Ranking**: relevance (BM25, normalised over the candidates) first, then bounded factors:
  recency for episodes (a floor, so an old memory that matches well still comes back), importance
  1–5, the subject named in the query, the profile. The old multiplicative half-life sent an old
  episode to almost zero whatever it matched (LoCoMo turns: R@5 19.8 %).
- **Time in the question**, read by code: explicit expressions only, widened by a margin, used to
  reorder (memories dated in the range first, and facts true then), never to filter; with a past
  range, facts no longer true are candidates too.

### 4. Durable state, compiled from raw history (PR 4)

Raw history is kept for audit: the conversation in eriphe-core's database, every change in
`memory_log`, the journal episodes. What the brain needs to act is compiled from it, in four kinds of
durable state: **facts**, **decisions**, **progress** and **lessons**.

- `decision` (new kind): a choice and its reason, in one sentence ("X plutôt que Y, parce que Z").
  A reversal supersedes it, so the old decision stays dated.
- `progress` (new kind): one current card per project or goal (subject `project:x`, `client:x`),
  with a state (`active`, `blocked`, `done`, `paused`) and a text that says what is done, under
  way, blocked and left. Each change supersedes the previous card: the chain is the history, the
  head is the state. The entity cards of the next phase render it.

These are kinds rather than new tables because they need exactly what memories already have (time,
supersession, trust, the log, full-text search), and the extraction already writes kinds.

The extraction also gives, per memory: `importance` (1–5), `keywords`, a planned end
(`valid_to` for what is temporary: "en congés jusqu'au 27"), and a **basis**: `stated` (said in so
many words), `inferred` (deduced), `ambiguous` (unclear). The basis is beside the trust, not in
place of it: trust says *who* it comes from, basis *how sure the reading is*. Of what comes from the
user's own words, only a stated memory is active at once; an inferred or ambiguous one is
proposed (journal episodes, summaries by nature, stay active as before).
Relative dates are resolved against the segment's own date; a soft deadline is not an end of
validity; a newer statement does not contradict an older one by being newer.

**Task receipts.** A finished task's turn ends with a structured receipt (what changed, what was
verified, decisions, what needs approval, lessons; the reliable-tasks work, `core/receipt.go`),
which the transcript gives the extraction in the task's segment. It becomes durable state like
anything else a task says, with external trust: its decisions are `decision` memories, its state
of the work the project's `progress` card, what needs the user's approval an intention (what the
user owes the task), and its lessons `lesson` memories, the only lessons an extraction may write,
and only ever proposed: lessons stay the user's to endorse. Reading `task_harness.receipt`
directly, rather than its text in the transcript, is a later refinement once those PRs land.

### 5. Typed intentions, evaluated by code, that wake the brain (PR 5)

A table `intentions`: what (text), kind (`promise`: the user owes someone; `commitment`: Eriphe
owes the user; `waiting_for`: someone owes the user; `reminder`), who owes, to whom, the trigger
(`time`, or `person`: "next time I talk about Hélène"), when it is due and when to remind, the
state (`proposed → open → due → done / cancelled`, `snoozed → due`), provenance and trust.

- **Form and Revise** are language tasks: the nightly extraction creates intentions and sends
  typed patches (done, cancel, reschedule) for existing ones, citing its sources; the brain does
  the same through tools during the conversation.
- **Filter and Decide** are code: every minute, without a model, open intentions whose reminder
  time has come, and snoozed ones whose snooze is over, become due; a person cue becomes due when
  the user's message names that person.
- **Wake**: an intention that becomes due wakes the brain through a new `clock` trigger of the
  assistants (the brain has it by default), with the wake packet: the due intentions, the profile,
  the last day. The packet existed but nothing called it, because an assistant had only the
  `conversation` trigger. It wakes on transitions only, never periodically (PM-Bench).
- **Trust**: an intention from external content (a mail that says "pay before Friday") is proposed
  until the user approves it; approved, it may remind, never act: the wake turn that reads it is
  tainted, so it can neither allow a permission nor lend tools that write.
- The old `commitment` memories move into the table (state kept, the memory archived with a log
  entry that names the intention): one place for what is owed.
- Each intention wakes the brain once when it comes due, and once more at its deadline if it is
  still due; after that it stays listed (pack, page, `intentions`) until closed. Quiet hours and
  "now, the brief, or nothing" are the interrupt score's, a later phase: for now a due reminder
  wakes the brain at any hour, as the user set it.
- Event cues other than a person ("when the PR is merged", "when the client answers") are not
  checked by code yet: PIS needs a language judge for them, and PM-Bench shows channel monitoring
  is where agents fail most (16.7 % at best). They stay the brain's to notice, and the next phase's.

### Order of the work

The plan had four PRs; the owner's later input (decisions and progress as durable state) and the
provenance labels made the extraction's new schema a topic of its own, so it is split from the
retrieval PR: 1 the evaluation, 2 the poisoning fix, 3 retrieval, 4 durable state (decisions,
progress, basis, importance, planned end), 5 typed intentions. Each is stacked on the previous.

## Where we deliberately differ from the strongest systems

- **No vectors by default.** Zep, Mem0, OpenClaw and LongMemEval's best retrievers use dense
  embeddings, which win on paraphrases. On a small, personal, French corpus the evidence says
  structure, time and the key matter more, and an embedding model costs 150–400 MB of RAM on a
  machine without AVX. The optional tier (local embeddings, RRF fusion) is turned on only if,
  with stemming and keys, recall@5 on paraphrased questions stays under 80 %.
- **No model per message.** Mem0 and Zep call a model on every write, Honcho derives per message,
  Mastra observes every 30 k tokens. We extract once a night (and after a busy, quiet day), so the
  cost is a few tens of thousands of cheap tokens a day, and nothing waits for a model in a turn.
- **No graph database.** Zep's bi-temporal edges are kept as dated rows with supersession and a
  log; the entity links of the next phase are a table.
- **Prospective memory is first-class.** None of Mem0, Zep, Letta, OpenClaw or Hermes has typed
  intentions; PM-Bench shows that retrospective stores do not solve it, and Typed Intention
  Stores that a table plus code does.
- **Provenance is enforced, not advisory.** Trust decides what may be active and what a turn may
  do; OpenClaw's taint gate is the closest, and only for consolidation.

## Results

Baseline, before any change of PR 2–5 (`go test -run MemoryEval -v`, and the opt-in benchmarks):

| Measure | Raw | Tool |
|---|---|---|
| Our corpus, recall@k, all retrieval questions (76) | 72.8 % | 80.7 % |
| — extraction / multi-session / temporal / update | 96.0 / 44.4 / 30.8 / 78.6 | 96.0 / 44.4 / 76.9 / 78.6 |
| — decision / progress | 100 / 80.0 | 100 / 80.0 |
| Prospective Set-F1 (commitments and the wake packet) | 77.6 % (TP 26, FP 12, FN 3) | |
| Interrupt or not (Eriphe never interrupts on its own) | 5 / 20 right, 0 of 7 interruptions | |
| Injection cases | 9 hold, 4 known gaps (the poisoning of PR 2) | |

| Public benchmark (retrieval only, FTS5, no model) | R@5 | R@10 | Published, for reference |
|---|---|---|---|
| LoCoMo, dialogue turns (1,536 q, categories 1–4) | 19.8 % | 24.8 % | DRAGON, dialogue: 58.8 / 67.5 |
| LoCoMo, the dataset's observations | 47.8 % | 53.4 % | DRAGON, observations: 49.6 / 57.1 |
| LongMemEval_S, turn (470 q, abstention aside) | 65.2 % | 74.5 % | BM25 round, K=V: 47.2 / 53.8; +fact 55.4 / 60.8 (Table 9; dataset variant not stated) |
| LongMemEval_S, session (sessions of the top turns) | 85.1 % | 87.9 % | BM25 session, K=V: 63.4 / 71.0 |

After PR 2: the 13 injection cases hold, no known gap; retrieval unchanged.

After PR 3 (same corpus, same questions; "text only" seeds the memories without the keywords and
importance an extraction gives):

| Measure | Before | After, text only | After, with keywords and importance |
|---|---|---|---|
| Our corpus, recall@k, all (raw / tool) | 72.8 / 80.7 | 84.6 / 86.0 | **90.8 / 92.8** |
| — multi-session (raw) | 44.4 | | 75.0 |
| — temporal (raw / tool) | 30.8 / 76.9 | | 76.9 / 84.6 |
| — update (raw) | 78.6 | | 92.9 |
| LoCoMo turns, R@5 / R@10 | 19.8 / 24.8 | **49.7 / 57.1** | (DRAGON 58.8 / 67.5) |
| LoCoMo observations, R@5 / R@10 | 47.8 / 53.4 | **50.4 / 55.5** | (DRAGON 49.6 / 57.1) |
| LongMemEval_S turn, R@5 / R@10 | 65.2 / 74.5 | **70.8 / 79.2** | |
| LongMemEval_S session, R@5 / R@10 | 85.1 / 87.9 | **90.0 / 91.5** | |

On LoCoMo's observations (an extraction's facts), lexical search with these factors now matches
the dense retriever of the paper at R@5; on raw dialogue turns it is still 9 points behind it. The
remaining misses on our corpus are paraphrases without a shared word ("proches" for a partner and
a sister, "vivait" for "habite") and lists that need more than five memories: what the optional
embeddings tier, or entity cards, would address.

After PR 4: decisions and progress cards are kinds of their own (the corpus's seeded as such),
retrieval unchanged at 90.8 / 92.8; what the extraction writes for them, and the basis rules, are
covered by `TestExtractionDurableState`. How well a real extraction fills them is measured by the
opt-in model run.

After PR 5: prospective Set-F1 **100 %** (TP 33, no false alarm, no miss, 27 checkpoints with a
person cue the old model could not express), against 77.6 % for commitments and the 24-hour wake
window (12 false alarms: reminders a day early, snoozes ignored, done things still due). This
measures the lifecycle, which is code: the scenario gives the intentions as a good Form step would.
How well a model forms and revises them from the conversation is the opt-in model run. Injection
cases: 16 hold (three new on intentions), no known gap.

**With a model** (opt-in, `ERIPHE_EVAL_MODEL_CMD='claude -p --model haiku'`, one run each, 85
questions, exact keys; 2026-11-01). "Seeded": the corpus's memories as a good extraction writes
them, then `recall` and the answer. "End to end": the real nightly extraction replayed over the six
weeks (about 40 runs), then the same questions over what it wrote.

| | Before (PR 1's code) | After (PR 5's code) |
|---|---|---|
| Seeded, all | 84.7 % | **90.6 %** |
| — multi-session / update / progress | 50.0 / 78.6 / 80.0 | 66.7 / 100 / 80.0 |
| End to end, all | 83.5 % | **90.6 %** |
| — extraction's operations rejected | 3 | 0 |
| The injected mail ("send the invoices to …") believed | no | no (only the task's external journal episode mentions it) |

Read with care: one run per side with a small model; an earlier run of the same "after" code
scored 92.9 % on both, so a couple of points either way is noise. Some misses are the keys' fault
(a right answer worded otherwise), the same on both sides. What remains is mostly lists that need
more than the memories found ("who are her relatives": only the sister), the paraphrase gap of the
lexical search, and two answers the model declined ("INCONNU") with the memory in front of it.

Read with care: LongMemEval's Table 9 may be on another haystack size than S, our session score
ranks turns and not sessions, and both benchmarks are English (the French stemmer and the plural
*s* still apply to English words, roughly).
The LoCoMo turn score is where our ranking failed: conversations span months and the episode
half-life crushed old turns that matched.
