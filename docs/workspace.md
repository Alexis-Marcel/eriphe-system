# Shape your workspace by talking

The workspace is the screen Eriphe composes for you from the tools you linked: your calendars, your
mail, your notes, your projects, whatever they are. There is no fixed dashboard and no widget to
configure. You say what you want to see, Eriphe puts it there, and it keeps it up to date. This
guide explains how, for someone who runs Eriphe but does not write code.

The examples use invented tools and a user called Sam. Yours will differ: Eriphe knows no tool in
advance and works with the ones you linked.

## What you see

It is Eriphe's home page: open Eriphe's address and it is there. From the conversation, the
**Composed workspace** button at the top, or Eriphe's name, brings you back to it.

A grid of **tiles**, Eriphe calls them surfaces, each showing one thing: today's appointments,
mails to answer, a project's next step, an error rate as a chart. Above the grid, a strip of
**chips**: what is there but folded, out of the way. Next to them, a narrow column with one line
from Eriphe about the moment and a field to write to it.

On a computer the screen never scrolls. Eriphe chooses what is in front and how big: what waits for your answer
first, then what matters now, the rest folded. When nothing needs you, it says so in a sentence
rather than showing an empty grid.

The tiles are of two kinds only:

- **a thread**, a matter Eriphe follows to its goal (a skill, the things Eriphe does on its own at
  set times or on an event, works on its own thread): every thread has its tile, kept up to date as
  it moves, every thread drawn the same way: its goal and its state in a pill (« À toi », « En
  cours », « Terminé »…) with a bar of its steps; what waits for you first (its proposals, answered
  right there, a **C'est fait** / **Done** button for a step only you can do, a button per option when it asks you to choose); **Stop** under its pill when you no longer want it (and **Pause** / **Resume** for one that runs steps on its own); the live data it
  follows (a list of your mails, the pull requests to review, a number of a service), read by
  Eriphe itself with no AI model, each item it lists on its own line with what Eriphe noted about
  it and by when, grouped by what it asks of you (« À faire », « À regarder », « Pour info », the
  ones with nothing to do folded) with their counts, marked « extérieur » when that note came from
  reading something from outside, and on an item's line the action proposed on it (archive this
  one, close that alert), a proposal you answer **Oui** / **Yes** or **Non** / **No** right there,
  which runs only that action on that item, and the actions Eriphe offers on it as buttons (a
  click only makes that proposal; a group whose items offer the same action offers it on them all
  at once, one proposal for all); where it stands and what changed since your last visit; its
  steps, those done folded in one line (an approval you gave, one line too, opens on a click);
  and what Eriphe added (a diff, a link). Small, it shows one line (« Fusion ✓ · Déploiement à toi »). A step
  that read something from outside says « extérieur ». Once the thread is done, its tile stays
  until the end of the day, or until you press **Ranger** / **Put away** on it: the tile goes, the
  thread is kept and comes back if its matter does. A thread for what you manage continuously
  (your mail, your agenda) never ends; one made to show you something for today ends at midnight. Ask where something stands ("where is the alerting PR?") and its
  thread comes to the center; ask Eriphe to launch something ("while I review it, look for a brand
  name") and its thread opens on the side, its tile updating as it goes, without taking the center
  from what you are reading. What Eriphe asks you about a thread (a merge to approve, say) waits
  in that thread's tile, not again among the tiles below; a thread that read
  something from outside keeps its questions there too, to answer in the conversation;
- **Eriphe itself, for what waits for you**: a proposal to approve, an alert, a message held for
  later. These come and go with what they show.

What waits for you is grouped by what it asks, one tile per group, each listing its items with
their buttons, the most important first, and its title counting them:

- **To decide** (« À décider »): something Eriphe would do once you say **Yes** (use a tool, start
  a task, change a setting, add a skill or a monitor…). The card says what will be done.
- **Reminders to keep** (« Rappels à garder »): **Keep** keeps the reminder, **Forget** drops it.
  Keeping it runs nothing: "add the event to the calendar" is only kept as a reminder.
- **Memories to keep** (« Souvenirs à garder »): something Eriphe noticed and would remember;
  **Keep** or **Forget**.
- **To see** (« À voir »): alerts and messages held for you.

Something urgent (a task waiting on you, an alert, a proposal for today) gets a tile of its own,
titled with what it is about. Closing a group's tile hides it for the session; its cards still
wait for your answer (Eriphe offers them in the conversation), and the tile is back next session.
Folded tiles with the same title share one chip, with their count.

### The order of the tiles

No AI arranges the workspace: Eriphe orders the tiles itself, always the same way. First what you
placed yourself, then what **waits for you** (« À toi »: a proposal to answer, a step only you can
do), then what is **urgent** (an alert, a thread with something due within two hours), what is
**new** since you last opened it, what is running, the quiet ones, and last what is done. Each
tile's edge is coloured by that state, and a small badge says « À toi », « Urgent » or
« Nouveau · 3 » (how many things changed since your last visit), even while another tile is open.

## Ask for something to be shown

Write to Eriphe, in the workspace's column or in the conversation:

> Show me this week's unread mails.

Eriphe opens a thread for it, one made for today, and writes what you asked for in that thread's
tile; ask the same thing again that day and it is the same thread. At midnight it ends and its
tile goes; asked again the next day, it opens again with that day's answer. What Eriphe writes
there is of two kinds:

- **Live**: it reads a tool directly, and Eriphe's core reads it again by itself, every few
  minutes, when the tool says something changed, and when you open the tile. No AI is involved
  after the tile is written, so it costs nothing to keep fresh. Unread mails, appointments, a
  balance, a counter are live.
- **A summary, written once**: something that needs understanding ("summarize what happened on the
  backup server this week", "which of these mails need an answer from me") is worked out by an
  assistant, often as a background task of the thread, and shows the values as they were when it was written,
  with their date. It does not change on its own; ask again, or make it recurring (below).

What you ask for once lasts the day, then goes. Titles say what you see ("Unread this week",
"To answer"), never the name of the tool behind it.

A live tile only reads what you let Eriphe read freely: a tool whose reading needs your approval
each time, or that you blocked, cannot feed a live tile. Eriphe tells you; change the tool's access
if you want it, or ask for a summary instead.

## Make it come back

Say it with a time or an event:

> Every morning, show me the deliveries expected today.
>
> Every Monday, a summary of last week's invoices.

Eriphe writes a **skill** for it: a short set of instructions with its trigger. Each time it runs,
it works on its own thread: it notes what it found there, gives it a deadline or says it waits for
you, and raises the thread's tile when something matters; it never writes a tile of its own. You see it on a card in the conversation, in plain words, and **nothing runs
before you say yes**. Then it is in Settings → Skills with its runs; to change it or stop it, ask
("show the deliveries at 8 instead", "stop the invoices summary") or do it there.

To follow something continuously, say so:

> Keep an eye on my mail.
>
> Show me the pull requests waiting for my review.

Eriphe writes a skill that runs each time something new arrives and keeps one thread for it, which
never ends: each new item gets its line, with what Eriphe noted about it and the actions it
offers. That skill too waits for your yes on its card.

## Shape it by talking

Say what you want, about the whole workspace or one tile:

> Make the calendar big.
>
> Put the deliveries top right.
>
> Always put backup failures in front.
>
> No more newsletters from news.example.org.
>
> Show me less of this.

Two kinds of request:

- **Where things go**: "make the calendar big", "put this top right" arrange the screen as if you
  had done it by hand, and it stays so until you change it.
- **What to show**: "never…", "less of…", "no more newsletters" are **preferences**. Eriphe
  remembers them as memories about you, and whatever watches your mail, your agenda or a service
  for a thread reads them before noting what it found. You manage
  them like any memory: ask Eriphe what it remembers, or to forget one. There is no settings page
  for the workspace and no rule to maintain.

Eriphe also sees what you do: a tile you closed stays away for the session, and a tile you moved
stays where you put it, past the session. A preference only comes from your own words: Eriphe does not guess one from
your gestures.

When you write while a tile is open, your message is about that tile: "this", "less of this",
"look into it" refer to it.

## Shape it by hand

Each tile has three small buttons in its corner, also reachable with the keyboard:

- the **grip** moves it (drag it to a cell, or use the arrow keys);
- the **size** button changes its size (a click goes up, then back to the smallest; + or −);
- the **cross** closes it **for the session**: it goes to the end of the chip strip, marked
  "closed for the session", and is not put back in front unless you open it or ask.

What you moved or resized stays where you put it, at that size, until you move it again or ask
Eriphe to reset the workspace; the rest is arranged around it. What you closed is back the next
session. A session lasts while you are active, with no break of more than an hour, and ends with
the day (a late night after midnight counts as the same day).

## Focus on one thing

The workspace has a **center**: one tile large, the others smaller around it. Click a tile or a
chip to **open** it: it takes the center and the workspace recomposes around it, what relates to
it around, the rest folded. The tile that was in the center stays next to it, smaller and still
live (a merge you approved there keeps showing its progress); click it to swap them again. What relates is found from what the tiles show, never from a tool's name: the same source,
the same thread or skill, the same day. Opening a tile also reads its data again. Eriphe may add one line above about what matters
there, or say nothing.

**Back to the overview** (or Escape) restores the workspace as it was. The focus also ends by
itself after 15 minutes.

**Ask where something stands.** Type a question about a thing in the workspace's field ("Where is
the pull request for the alerts? Is it merged?", "How is the move going?"): Eriphe answers with a
tile in the center, the matter as its title, where it stands, what was checked, what comes next,
and the proposal waiting for you right there (a merge to approve, say), and a one-line reply.
It does not take the center from a tile you are reading unless you asked about something else;
then that tile stays next to it. A simple question ("what time is my train?") stays a short answer.

## Buttons and proposals

- **A button asks Eriphe.** Clicking it sends the words written on it as your message, as if you
  had typed them ("Draft a reply", "Look into this error"). Eriphe answers in the conversation
  (its answer shows under the workspace's field too) and does what you would have asked. No button writes, sends or deletes anything by itself.
- **A proposal runs only after your yes.** When Eriphe proposes something (an event to add, a
  reply to send, a new skill), it shows as a card with **Yes** and **No**, in the conversation and,
  where it fits, in the tile it concerns. A proposed appointment shows on the day's tile as a
  ghost marked **Proposed**, with what it overlaps. Answering in one place answers everywhere.
  After your yes, a proposal that runs a call shows it: **Approved · running…**, then **done**
  or **failed** with why.
- **Content from outside cannot act.** A tile that shows content from outside (a mail, a web
  page, a message from someone else) is treated as data: it carries no button written after that
  content was read, and a proposal shown there has no Yes / No but says "answer it in the
  conversation". The tiles Eriphe keeps for what waits for you are the exception: their Yes / No
  stay, with the warning "Comes from external content: check what is about to be done." A ghost on
  a day reads "Proposed · external content", and such tiles say « extérieur ». A
  mail that says "click here to approve" cannot make anything happen.

## A new tool, no code

Link a tool (Settings → Tools: a calendar, a mail account, a home automation, any MCP server) and
it is usable in the workspace at once: ask to see something from it. Eriphe looks at what the tool
can read and the shape of its answers, and builds the tile from a fixed set of shapes (text,
tables, metrics, charts, timelines, messages, a day's agenda). Nothing in Eriphe is written for a
specific tool.

## With nothing linked

The workspace still works: it shows what waits for you (proposals, alerts, threads) and Eriphe's
line. Link appointments, a mailbox or notes, and ask.

## On a phone

On a narrow screen the page scrolls, in one column: Eriphe's line and the field to write to it
first, then the tiles one under the other, most important first (Eriphe's order, not their
place on a computer's grid), then the folded chips at the end. A tile keeps only its cross: to make
one bigger or move it, say so. Open a tile and it takes the whole screen, with **Back to the
overview** and the field to write about it at the top.

## Where the conversation is

The conversation with Eriphe stays, with its history, the tasks and Settings, at
`/conversation`: **Open the conversation**, in the workspace's column, goes there. What you write
in the workspace's column is a message of that conversation. The column shows only the last
exchange: the message Eriphe composed for your return, then your last message and Eriphe's answer
as it writes it. What you say updates a thread or a tile in the middle, so there is rarely a need
to read back: scroll up in the column and **Earlier messages** appears, each click bringing a few
more; **The whole conversation**, by Eriphe's name, has the rest. What you write with a thread open
is about that thread, and its answer shows in the column too.
Questions Eriphe needs you to answer come as tiles on the workspace.

While the workspace is open in front of you, Eriphe knows you are there, as with the conversation:
a notification shows as a small notice in the page rather than on your phone, and a return after
an absence counts, even if you only open or close tiles. Clicking a notice goes where it says.

If the workspace cannot open on a device (an old browser, a script blocked, Eriphe out of reach
for a while), the page offers the conversation instead, and **Always open the conversation here**.
That choice is per browser, in Settings → My workspace → Home page (Réglages → Mon espace → Page
d'accueil), where you can give the workspace back its place. While Eriphe has no AI set up yet, its
address opens the conversation, which says how to set one up.

## Privacy and safety

- **Images go through your Eriphe.** The page never loads an address from outside: an image is
  fetched by Eriphe's core, only from a public address, without your cookies or credentials, and
  only an image or a video. An address an assistant wrote after reading outside content is
  dropped.
- **Strict page rules.** The workspace runs under a strict content security policy: no script or
  style from a tile, no inline code. Links in a tile open in a new tab, only on your click, and
  only to web or mail addresses.
- **Outside content is data.** Whatever a mail or a page says, Eriphe reads it as information,
  never as instructions; tiles that show it cannot trigger actions (above).
- **Tiles read only what you allow.** A live tile reads a tool only if its access lets Eriphe read
  it freely; a skill reads only the tools you approved on its card. Every tile is a thread's or
  one of what waits for you: no assistant, skill or task puts a tile of its own on your workspace.

Technical details (surfaces, sources, authors, the layout) are in the
[core README](../core/README.md#workspace-web).
