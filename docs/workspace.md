# Your workspace: the graph

The workspace is Eriphe's home page: everything Eriphe holds for you, drawn as a graph. There is no
fixed dashboard and no widget to configure. You say what you want, Eriphe puts it there and keeps it
up to date. This guide explains how, for someone who runs Eriphe but does not write code.

The examples use invented tools and a user called Sam. Yours will differ: Eriphe knows no tool in
advance and works with the ones you linked.

## What you see

Each thing Eriphe holds is a **bubble**. A bubble on its own is a **subject** (« Substack », « My
day », « Mails »), drawn larger; the work done for it grows around it as smaller bubbles (« Plan
of the first article », « Style of the articles »). A thin grey line joins bubbles that relate to
each other.

How a bubble looks says what it is, never chosen by an AI, one sign for each meaning: what waits for
you is brighter, with a breathing violet halo (out of view, a small arrow at the edge of
the screen points to it: click it to open it), what is running turns and says where it is under its name (« 3 articles read »), what is
done or old fades. A subject that watches a figure you asked Eriphe to keep an eye on (« tell me
if my PEA moves more than 2 % ») becomes a small **tile** when the figure moves past that: its
name, the variation with ▲ or ▼, and a line of its recent values, in plain greys, updated as the
figure changes. Open it and it is a circle again, until the figure falls back and moves past
again. At most three tiles at once, the largest moves first; a subject that waits for you keeps its
circle and halo. A bubble whose title could be someone else's words (a mail's subject, say) is
named **Outside content**: its own words are never shown as a title.

At the bottom, the **bar**: the one field to write to Eriphe. The bar is only a bar: no history,
no buttons but its paperclip. The conversation is the subject's page: what Eriphe has to say at
length, its questions and the buttons that answer them are written there.

**Attach files** to a message with the paperclip (on a phone it offers the camera and the photo
library), by dropping them on the bar, or by pasting an image (a screenshot) into it: images (PNG,
JPEG, GIF, WebP), PDFs and texts (`.txt`, `.md`, `.csv`), five at most, 20 MB each. They show as
chips above the bar, a ✕ each, until you send; files alone are a message too. While the message
waits for Eriphe, the bar shows their names with it. Eriphe reads them when it reads the message.

A photo or a PDF can stay with a subject: send the photo of a shirt with « ajoute cette chemise à
ma garde-robe » and Eriphe keeps it with the shirt's node, tagged; its page shows the photo (a PDF
as a link to download it), and a page listing your clothes shows each one's photo as a thumbnail.
A node keeps 20 files of 5 MB at most: Eriphe tells you when a photo is too large.

## Eriphe's orb

Eriphe is the small blue orb resting in the bar. It only answers: it leaves the bar in reply to
what you said to it (a message, or your **Yes** or **No** on a card: what came of it, « Done »
or a failure, it says like any answer), never on its own (what starts waiting for you without
you asking, a mail a monitor found, shows on its bubble; what Eriphe says of it unasked is kept
in that subject's trace). Then it goes **beside the subject** it is about, its bubble or its open page, where it
covers nothing (no bubble, no title, no page, not the bar), and follows it if it moves. When the
screen is too full for that, it stays in the bar and says it in a line above it:

- **at work**, it pulses, with the first words of what it is writing;
- **when it is done**, it says in a few lines what it did; **See more** opens the subject's page,
  where the rest is: the orb never repeats the page. After a few seconds it goes back to the bar;
  its ✕ sends it back sooner;
- **when its answer asks for your yes on a card**, it sits beside that page, at the card's height,
  and **Show** brings the card into view;
- **an answer about no subject** (« do you have access to my films? ») shows at the home, whole,
  in a free spot, and stays until you write again, open a subject or close it;
- **if Eriphe could not answer**, it says so, with **Try again** and a link to Settings → Agents.

Words Eriphe wrote after reading outside content carry a small amber dot. A title that may be
someone else's words shows as it is, never replaced; on its page it is marked with a small ⚠. Click the orb to open the subject it is
beside. With reduced motion set on your system, it goes at once instead of gliding; a screen reader
reads what it says. On a phone it does not move: its words show as a line above the bar, naming
the subject (« On Plan of the first article »).

## Open a page

Click a bubble: its **page** opens right there in the graph, and the graph makes room for it. Hold ⌘ (Ctrl on Windows and Linux) or shift as you click to open one more page
beside those already open. Each page closes on its own cross.

A page shows:

- what it belongs to, its title, and when it changed (« 1:13 pm », « yesterday, 1:13 pm », « Nov 19 »,
  or « Running » while it runs) on the title line;
- what Eriphe wrote there, text and live blocks (a list, a table, a chart…) whose values keep up
  to date by themselves;
- behind its **⋯** menu, rarely needed: its **Trace**, what was done for it, when and by whom (you,
  Eriphe, a task, Eriphe's code, outside), and its **.md file**, the page's text as it is stored, a plain text file versioned by git, which you can edit
  there and **Save** (⌘S or Ctrl-S). If a newer version came while you were writing (Eriphe or a
  task wrote meanwhile), nothing is saved: the page shows that version, and you take it or keep
  your text to save it over;
- its **versions** (v1, v2…): an older one shows as it was;
- the cards that still ask you something, where Eriphe placed them in the page, else at its foot:
  what a yes makes in plain words first, the technical details (sources, monitor, trigger, the
  skill's instructions) one click away under **Details**. A card in the page is drawn by Eriphe's
  code from its records, never from the page's words: once answered it says **Approved** or
  **Declined**, and **Proposal withdrawn** when it went with its archived page; a card that is not
  this page's shows as **Card not found**, with no Yes. What is decided leaves the page's foot: the
  **⋯** menu lists it under **Already decided** (Done, Set aside), with **What keeps it alive** (the
  skills that maintain it, their monitors and its sources);
- **Archive**: see below.

A page is like a window: its **enlarge** button (or a double click on its title line) gives it most
of the screen, its text at a width that reads, a table or a chart the whole width; its corner,
dragged (or its arrow keys), gives it a size. Sizes last for the session; the graph makes room
around the page.

A page that follows things (your mails, the alerts of a service) reads as a **bilan**: a sentence
or two on top, what waits for you first, then **For info** in a few words; under it, « To handle »,
one line per item saying what Eriphe proposes, with its one **Yes** (No is a discreet ✕ on hover);
what goes as a batch (« 38 useless mails to throw away ») on one line with the one **Yes** of the
card they share, which you open to untick what you keep. A line clicked opens the item as a letter: who, when, its
subject, its text, then **On Yes** and what it sends (the reply itself), the exact call under one
**details**. Done, an item leaves its section.

The bar talks to the page you opened or clicked last: « Write to Eriphe about Plan of the first
article ». With no page open it talks to the home. Close every page, or click Eriphe's name, to go
home. The page the bar talks to becomes the centre of the graph: the graph glides round it, the
other bubbles and pages spread all round it, and the view shows it in the middle at its full size
(the neighbours may go past the screen's edge; what waits for you out of view keeps its arrow).
This can be turned off in the workspace's settings (below): the view then frames every open page
when they fit readably, otherwise the one the bar talks to; the others stay open where they are.

A link may open a page directly: `/espace?open=node-<id>`. When Eriphe wants to show you something,
it opens its page beside yours; the bar stays where you were: only your own click moves it.

When a work Eriphe ran ends, what it made for you (its last step, « Plan of the first article »)
waits for you, the work around it done. It stops waiting when you say it suits you (« Ce plan me
va »: Eriphe marks it done) or when you archive it. A page counts its versions from the first with
something in it: a step opened empty for its task to write starts at v1 with the task's text.

On a phone the graph gives way to a list: each subject, its bubbles under it, in the order they were
made, what waits for you shown by its bright dot, what runs by its line. A tap opens its page over the list; closing it
brings the list back. The bar is the same.

## Move things

Drag the background to look around, scroll or use two fingers to move, hold ⌘ or Ctrl with the
wheel to zoom; a double click gives the view back to Eriphe. The graph is a force graph, like
Obsidian's: everything is pulled to the centre, everything pushes everything else away, and two
linked things (a bubble and its subject, two subjects a link joins) hold together like a spring.
An open page is a node like any other, only bigger: a subject and a page it links to end up side
by side. Each link is a thin grey line. Any change (a bubble come or gone, a page opened or
closed) sets the graph moving until it settles again, then it rests; a new bubble appears in free
space near what it links to. Hover a bubble or a page's title line: it, its neighbours and their
links stay sharp, the rest fades. Drag a bubble, or a page by its head: what is linked to it comes
along. Drop it: nothing is pinned, the graph settles again from where you left it. Bring a bubble
to the edge of the screen and the view follows.

The workspace's own settings are under the gear at the top right: the graph's four forces as
sliders (**Centre**, **Repulsion**, **Link force**, **Link distance**), **Default** to give them
back, and **The open node becomes the centre** (on unless you turn it off). They apply at once,
the graph moving as you set them, and are kept for every screen you use. **All settings** leads to
the rest of Settings.

Where things are holds while the workspace is open, not after: a reload lays the graph out afresh.
Your Eriphe keeps the pages you left open and your light or dark choice (the sun or moon at the top;
until you choose, the workspace follows your system): they come back the next time, on any of your
screens.

## Archive and reopen

Only you archive. **Archive**, at the top of a page, asks once more and says how many bubbles go with
it: a bubble leaves the graph with everything under it, and what was running there stops. Eriphe
never archives by itself; ask it to and it tells you where the button is. **Archives** at the top
lists what you archived, latest first, each with **Reopen**: the bubble comes back with what was
archived with it.

In a page, a reply Eriphe offers (a button under its text) sends its words to Eriphe about that
page, as if you had typed them (that is how you answer its questions: « Do as you propose » takes
every default it wrote); it does nothing else, and does not work on an older version. A reply in a
page marked as from outside (the ⚠ beside the page's title) works too, but its words may echo what Eriphe read: Eriphe takes them as
coming from outside, not as yours, so they unlock nothing your own words would. No
button, box or mark in a page acts: what acts waits for your yes on a card.

## Ask for something

Write to Eriphe, in the bar:

> Show me this week's unread mails.

Eriphe first decides **where** the answer goes. A quick question ("what time is it in Tokyo?",
"what's my next appointment?") or a simple action is answered in one line by the orb, with no new
bubble. What is about the page you are on is answered there. Anything else is a subject: its bubble
appears within seconds, before Eriphe reads anything, saying what it is doing, and fills as the
work goes, written by the agent Eriphe launches on it. Eriphe orchestrates: it answers in one line
naming the page ("I'm on it in « My mails »"), so you can go on with something else, and does
itself only a quick read, a gesture of a call or two, a yes or no, a one-line fix; a whole page or
a series of writes is an agent's work. Eriphe answers by **showing**: the
subject's page opens in front of you with what you need to know, like a dashboard, and the
conversation goes on there. Asked from the home, the bar moves into that page; asked from a page,
the new one opens beside it.

Each exchange keeps the subject's page up to date: the questions you answered go, what you chose is written
under « What we decided », and what comes next shows. When a part of it moves on in a bubble of its
own (a research done, an angle chosen in « Choose the angle », a new version of the article), Eriphe
updates the main page too: its summary, its plan, what remains. Work Eriphe hands to an agent goes in a bubble under the subject you are on; a bubble that turns out to be part of another subject, Eriphe moves under it.

To have something done **on the side** ("run a research on this aside", "look at my mail and
analyze it, but on the side"), say so: Eriphe opens its bubble without showing it (under the page
you are on when it is the same subject, on its own otherwise), says in one line what it started,
and the bubble rises when it has something for you.

What it writes on a page is of two kinds:

- **Live**: a block reads a tool directly, and Eriphe's core reads it again by itself, every few
  minutes and when the tool says something changed. No AI is involved after the block is written,
  so it costs nothing to keep fresh. Unread mails, appointments, a balance, a counter are live.
- **Written once**: something that needs understanding ("summarize what happened on the backup
  server this week") is worked out by an assistant, often as a task, and shows what was found, with
  its date. Ask again, or make it recurring.

A live block only reads what you let Eriphe read freely: a tool whose reading needs your approval
each time, or that you blocked, cannot feed it. Eriphe tells you.

## Follow something: a page kept up

There are only bubbles. What you want to follow all the time ("keep an eye on my mail", "follow
the production alerts", "can you follow and manage my days?") is a page Eriphe writes at once, no
card: its blocks read their sources live, with sensible defaults (what it remembers refines
them), no questions before. Its page says its choices in a line ("I only tell you at the brief,
never at night") and offers the likely changes as buttons ("Add my mail"); you change them after,
never through a questionnaire first.

When the page needs work as things arrive (sort each new mail, prepare a reply, rewrite the bilan),
Eriphe proposes a **skill** that maintains it: the page is its home, and the skill may still act on
other bubbles. If what it reacts to needs a new monitor, the monitor is on the same card: one yes
makes both, and nothing runs before it. While a skill maintains it, the bubble wears a small
satellite on the graph, is never marked done, and its **⋯** menu says **What keeps it alive**; the
skill turned off, it is a bubble like any other. What you said matters to you is remembered as a
preference; when to tell you is a notification rule.

What you want to see once ("open the production for me"): Eriphe opens a bubble under the one you
are talking about, with a live block, and shows it beside your page. If you want to follow it, it
proposes the skill that keeps it up.

## Make it come back

Say it with a time or an event:

> Every morning, show me the deliveries expected today.
>
> Keep an eye on my mail.

Eriphe writes a **skill** for it: a short set of instructions with its trigger, which works on its
own bubble (or the page it maintains) each time it runs. As for a page, it first asks in one message what changes it (what it
covers, what matters, whether and when to tell you, what to prepare), with a default for each, unless
you already said. You see it on a card, recalling your choices, in plain words, and **nothing runs before you
say yes**. Then it is in Settings → Skills with its runs; to change it or stop it, ask, or do it
there.

Skills get better with use. When a piece of work that followed a skill had to correct its method
to finish (a step or a check was missing), it proposes a new version of that skill: you see why on
its card, and the diff in Settings → Skills. It changes only the instructions, never what the
skill may use or when it runs, and nothing changes before you say yes.

## Shape it by talking

"No more newsletters from news.example.org", "show me less of this" are **preferences**. Eriphe
remembers them as memories about you, and whatever watches your mail or your agenda reads them
before noting what it found. You manage them like any memory: ask Eriphe what it remembers, or to
forget one. There is no rule to maintain: the workspace's only settings are how the graph moves
(see Move things).

## Proposals

- **One way to say yes.** Eriphe proposes one thing per item, and you say **Yes** (a discreet ✕
  declines). Whatever asks your yes is a card, in its page or on the line of the item it is about;
  to do something else, say it in the bar. Answering on a card, in words, from a channel or on a
  page in Settings is the same answer, and the card says so everywhere.
- **A proposal runs only after your yes.** When Eriphe proposes something (an event to add, a reply
  to send, a new skill), it shows as a card with **Yes** and ✕, in its page. A card that
  belongs to no page shows above the bar, wherever you are, until you answer it: no card waits
  out of sight (with the paperclip, the only buttons the bar ever has). A proposed appointment shows in the agenda of its day as a ghost marked **Proposed**, with its
  **Yes**, wherever that agenda is; on its page the agenda is then its place, not the page's foot.
  An invitation someone sent you carries the small amber sign of what comes from outside.
  Answering in one place answers everywhere. After your yes, a proposal that runs a call shows
  **Approved · running…** while it runs.
- **A decided card leaves the page.** Done, failed, declined or withdrawn, a card leaves the page's
  body, its foot and its rows; the page's **⋯** menu lists it under « Déjà décidé » with how it
  went (« Fait », « Échec » and why, in the tool's own words, marked ⚠ when they may be someone
  else's, « Refusé », « Retiré »). Eriphe's orb says what came of your yes, as it answers what you
  say: « C'est fait », or « Échec : » and why in plain words. A failed call is never tried again by
  itself, and there is no Retry button: Eriphe may propose it again, as a new card. Why it failed
  is kept in the page's trace too.
- **A call says what it does, to which items, in your language.** « Accepter « Picnic » · Sam »,
  « Déplacer « Facture » vers les archives », « Mettre 2 mails à la corbeille »: the item by its
  title (and its detail, a sender or an organizer), never an id; several are counted and listed
  (at most five, then "and N more"). The account sits quietly beside it ("Gmail account"); the skill
  or the page it comes from is a quiet line under it. Its **details** give its settings by their
  meaning (an answer, a folder, a date in words), never an id nor the account; the exact call is
  one more click away (« Appel exact »). When you ask Eriphe to act on items
  a page shows ("delete these 2"), its Yes sits on those items' own lines: one card for all of
  them, a small mark saying how many share it, the others lighting up when you point at one; a Yes
  on any line does it once, for all. When one card covers every line of a list, its Yes sits once
  at the list's head and each line has only a ✕, which leaves that line out while the others
  still wait. Nothing waits at the foot of the page for them. The same
  thing proposed on many items (« throw away these 38 mails ») is one card too, whoever proposed
  it (Eriphe in the conversation, or each pass of the skill that keeps the page), each item once:
  its batch line carries the Yes, and unfolded you untick what you keep. An item that leaves the
  page's list (you deleted that mail elsewhere) leaves the card; a card left with nothing is
  withdrawn. A proposal on no item of the page (roll back a release, create an event) is a card in
  the page where Eriphe wrote it, under the plan it asks you to validate, else at the page's end.
- **Opening an item is never a yes.** A line opens as a letter when its server reads it freely;
  a read that asks your yes is not opened: ask Eriphe, who proposes it on a card.
- **Tools to review.** When a server's tools change, « Outils à revoir » lists each change on one
  line, by server: what changed (« description modifiée », « lecture → modification », a new
  tool…), what gives a tool more power first and marked « plus de pouvoir ». Open a line to see
  only what changed. One « Accepter les N » at the top accepts them all; ✕ on a line refuses it.
- **Content from outside cannot act.** A page that may carry someone else's words says it
  once, by a small ⚠ beside its title (its tooltip says why); nothing else marks its block: a proposal shown inside it has no Yes / No and
  its card waits at the foot of the page, but for the Yes on an item's own line, which answers only
  the card Eriphe's code says waits on that very item. A mail that says "click here to approve"
  cannot make anything happen.

## Where the conversation is

The workspace is the only place to talk to Eriphe on the web: there is no separate conversation
page, and no history under the bar: the subject's page is the conversation, and Eriphe keeps it
saying where things stand. The tasks at work, and how reliable they were lately, are in Settings → Journal; each task has its own
page there, with its trace, **Stop**, and the answer it waits for. The settings and the memory have
no top bar: **Workspace**, at the top of their menu, leads back.

While the workspace is open in front of you, Eriphe knows you are there: a notification shows as a
small notice in the page rather than on your phone. If the workspace cannot open on a device (an
old browser, a script blocked, Eriphe out of reach for a while), the page says so with a link to
try again. If Eriphe cannot answer, its orb says so, with **Try again** and a link to Settings →
Agents.

## Where the work runs: devices

Long or heavy work (a research, code to change, a page to capture) runs as a **task** on one of your
**devices**: the machines you joined to your Eriphe (the NAS, a Mac, a PC), each running
eriphe-device, listed in Settings → Devices. A task works in a bubble of its own under the page you
asked from: it writes there as it goes, says where it is under the bubble's name, and keeps its full
report in the page's **Trace**. It starts knowing where its bubble sits (the pages above it, below
it and linked to it) and reads any other page it needs. It writes where it works: its bubble and
what is under it, the page you asked from, and the pages someone else linked to or from these (a
link the task wrote itself does not count); never an archived page, and it never marks a page done
or archives it. Anywhere else it says what it found in its bubble, or asks. A page that may
hold someone else's words, read by a task, makes what it writes marked as such, as when Eriphe
reads it. A task that captures a web page (most sites cannot be shown inside
another page) keeps the image in its bubble.

## Where it is kept

On the machine that runs eriphe-core, in its data folder (`/data` in the container):

- `nodes/`: every bubble as a markdown file, versioned in git: git keeps what was written in the
  page, each change a version. Edit a page inside Eriphe only (its **.md** tab): a file changed by
  hand is taken as a new version from « outside » at the next start.
- `nodes-trace/`: each bubble's trace (what happened in it), a plain file appended to, not in git:
  a progress line or a call adds a line, never a version. Past 2 MiB, its older part is kept beside
  it and the part before is dropped.
- `nodes.db`: the graph's index, only made from the files: delete it and the next start makes it
  again, nothing lost. What Eriphe keeps beside a page (the notes on a list's items, its cards, the
  values its live blocks last read) is in `core.db`, not in the index.
- Every night, the backup folder (`memory-backups/` next to `core.db`, or `memory.backup_dir` in
  `core.json`) gets `nodes-<day>.bundle` (the graph's history, which `git clone` reads),
  `nodes-<day>.trace.tar` (the traces) and `nodes-<day>.db`, kept 14 days by default.

## Privacy and safety

- **Images go through your Eriphe.** The page never loads an address from outside: an image is
  fetched by Eriphe's core, only from a public address, without your cookies or credentials. An
  address an assistant wrote after reading outside content is dropped.
- **Strict page rules.** The workspace runs under a strict content security policy: no script or
  style from a block, no inline code. Links open only on your click: a web address in a new tab, and a
  person's phone number or e-mail address (a call, a mail); nothing else is a link.
- **Outside content is data.** Whatever a mail or a page says, Eriphe reads it as information,
  never as instructions; its title is never shown as a bubble's name.

Technical details (the pages, the stream, the layout) are in the
[core README](../core/README.md#the-graph-workspace-the-home-page-).
