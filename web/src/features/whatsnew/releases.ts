/**
 * What changed in TeamUp, newest first, in words for the people who use it.
 *
 * Add an entry at the top with each update worth telling people about. Its id is
 * the day it went out (a second one the same day adds a suffix, 2026-11-12-b),
 * and a new id is what makes What's new open by itself, once, for everybody.
 */

export type Area = 'meetings' | 'chat' | 'tasks' | 'calendar' | 'code' | 'app';

export interface Change {
  area: Area;
  title: string;
  body: string;
  /** Where to try it, if there is one place. */
  link?: string;
}

export interface Release {
  id: string;
  /** The day it went out, as YYYY-MM-DD. */
  date: string;
  title: string;
  changes: Change[];
}

export const RELEASES: Release[] = [
  {
    id: '2026-11-16-d',
    date: '2026-11-16',
    title: 'Attendance: cameras, field work, review and export',
    changes: [
      {
        area: 'meetings',
        title: 'A stand-up can require cameras',
        body: 'Turn on Camera must be on in a stand-up. Whoever joins and never turns their camera on is In review, not Present, until somebody who reviews attendance decides.',
      },
      {
        area: 'meetings',
        title: 'Check in from the field',
        body: "When your organization turns on field work, Calls offers Check in from the field: a photo taken in the app and where you are, with a note. Today's stand-ups count you as in the field, not absent. Your manager and HR can see the photo and the place.",
        link: '/calls',
      },
      {
        area: 'meetings',
        title: 'Review attendance, and take it away as a spreadsheet',
        body: 'Give a role Review attendance under Roles, for HR. Reviewers decide days in review as Present or Absent, with a note that is kept with their name. Every attendance page downloads as Excel or CSV: people down the side, days across, and totals.',
      },
    ],
  },
  {
    id: '2026-11-16-a',
    date: '2026-11-16',
    title: 'Forgot your password? Reset it yourself',
    changes: [
      {
        area: 'app',
        title: 'Reset a forgotten password by email',
        body: 'Forgot password? on the sign-in page emails you a link to choose a new one. It works once, for 30 minutes, signs you out everywhere, and you get an email saying the password was changed. If your organization has no email set up, its administrators are asked in the app to reset it for you.',
      },
    ],
  },
  {
    id: '2026-11-15-e',
    date: '2026-11-15',
    title: 'Stand-ups ring, and leave stays quiet',
    changes: [
      {
        area: 'meetings',
        title: 'A stand-up rings as it starts',
        body: 'When a stand-up starts, everybody it is for is rung, like a call, in the browser and on their phone. Join takes you straight in, and Not now just stops the ringing. Nobody already in it, in another call, on leave or on a holiday is rung.',
        link: '/calls',
      },
      {
        area: 'meetings',
        title: 'Nothing rings you on leave',
        body: 'A call to somebody on leave does not ring them. Whoever called is told they are on leave, and a note waits in your conversation for when you are back. Meeting reminders skip you on your leave days too.',
      },
    ],
  },
  {
    id: '2026-11-15-d',
    date: '2026-11-15',
    title: 'Leave and holidays count in attendance',
    changes: [
      {
        area: 'meetings',
        title: 'On leave is not absent',
        body: 'A stand-up\'s attendance now shows On leave for somebody away, a whole day or half of one, and Holiday on the organization\'s holidays. Leave added after the meeting says who added it and that it came afterwards.',
      },
      {
        area: 'calendar',
        title: 'Plan leave, for yourself or your team',
        body: 'Add leave under Leave, including days already gone. Managers can add or correct it for the people they manage, and they are told. Choosing On leave as your status records it too. Administrators keep the holidays there.',
        link: '/leave',
      },
    ],
  },
  {
    id: '2026-11-15-c',
    date: '2026-11-15',
    title: 'From research to a branch, in one task',
    changes: [
      {
        area: 'tasks',
        title: 'Move a task into a project',
        body: 'Research or a bug filed with no project can move into one once it is that project\'s work, from the Project box on the task. It takes the project\'s key, TASK-7 becoming PHO-31, and keeps its comments, history and files. Anything that named the old key still links to it.',
      },
      {
        area: 'code',
        title: 'Start the branch from the task',
        body: 'On a task in a project, Start development creates its branch on GitHub or Bitbucket and puts the task in progress. You need write access to the project to do it; without it, ask for write access from the same place.',
      },
    ],
  },
  {
    id: '2026-11-15-b',
    date: '2026-11-15',
    title: 'Meetings nobody joined show as missed',
    changes: [
      {
        area: 'meetings',
        title: 'A meeting nobody joined says "Missed"',
        body: 'Once a scheduled meeting is over and nobody joined it, it shows as Missed, with no Join button. A 30-minute meeting at 5:00 is missed from 5:30; until then, whoever is late can still start it. The organizer can move it to a new time from the calendar, and the Missed filter on Calls lists them.',
        link: '/calls',
      },
    ],
  },
  {
    id: '2026-11-15',
    date: '2026-11-15',
    title: 'Drag tasks across the board',
    changes: [
      {
        area: 'tasks',
        title: 'Drag a card to change its status',
        body: 'On the Board, pick a card up and drop it on another column: To Do, In Progress, In Review, Blocked or Done. On a phone, hold the card for a moment, then drop it on one of the column names at the top. With a keyboard, press Space on a card, move it with the left and right arrows, and press Space again.',
        link: '/tasks?tab=board',
      },
      {
        area: 'app',
        title: 'Easier on a phone',
        body: 'Search is in the header on a phone too. The sections of Organization and Settings scroll to the one you are on, and team buttons no longer run off the screen. The calendar names your time zone in words, and removing a member or deleting a team now asks first.',
      },
    ],
  },
  {
    id: '2026-11-14-b',
    date: '2026-11-14',
    title: 'Stand-ups for your team',
    changes: [
      {
        area: 'meetings',
        title: 'A standing meeting for one team',
        body: 'A standing meeting can now be for chosen teams and people instead of everybody, and a team\'s manager can make one for their own team. Only the people it is for see it, on Calls and in their calendar, and only they are counted in its attendance. Somebody joining the team later is in it at once. Anybody else with the link knocks, and somebody in the team can let them in for that day.',
        link: '/calls',
      },
    ],
  },
  {
    id: '2026-11-14',
    date: '2026-11-14',
    title: 'See how the server is doing',
    changes: [
      {
        area: 'app',
        title: 'A System page for administrators',
        body: 'Organization, then System, shows how hard the server is working, updated every few seconds: processors, memory and disk, who is online, calls in progress and the biggest one, how quickly TeamUp answers, and when the last backup ran. Charts show the last 24 hours, and anything close to a limit, such as a full disk, a meeting near the size the server carries well or a missed backup, is called out at the top.',
      },
    ],
  },
  {
    id: '2026-11-13-b',
    date: '2026-11-13',
    title: 'Faster when everybody is busy',
    changes: [
      {
        area: 'chat',
        title: 'Conversations open quicker',
        body: 'Opening a conversation, and searching your messages, ask far less of the server, so both stay quick at the busiest time of day, even in a channel with the whole company in it.',
      },
      {
        area: 'meetings',
        title: 'Big offices fit',
        body: 'Everybody in an office shares one internet address, and TeamUp used to turn away the 101st person in a meeting or the app from the same office. An office of a thousand now fits.',
      },
      {
        area: 'meetings',
        title: 'Big meetings stay smooth',
        body: 'In a meeting, each person now receives only the cameras on their screen, at the size they are shown, and quality follows their connection. A meeting of 50 with every camera on needs about 3 Mbit/s each instead of most of a home connection, and a shared screen still comes through sharp.',
      },
      {
        area: 'meetings',
        title: 'Minutes keep up with busy days',
        body: 'When speech to text or the AI writing the minutes is busy, TeamUp waits its turn and asks again instead of losing what was said, so many meetings at once all get their minutes. The note-taker also listens to the meeting without taking its video, which leaves more room for everybody else.',
      },
    ],
  },
  {
    id: '2026-11-13',
    date: '2026-11-13',
    title: 'A safer TeamUp',
    changes: [
      {
        area: 'app',
        title: 'Harder to break into',
        body: 'After ten wrong passwords, signing in to that account waits 15 minutes. Changing your password signs you out everywhere at once, including anything still open.',
      },
      {
        area: 'chat',
        title: 'Deleted means deleted',
        body: 'Deleting a message deletes the files sent with it too, so they no longer turn up in the conversation’s files or in a search.',
      },
      {
        area: 'chat',
        title: 'Announcements are for administrators',
        body: 'Only people who administer channels can make an announcement channel, since everybody is in it and hears each post. In a direct message, neither person can remove the other or delete what they wrote.',
      },
      {
        area: 'app',
        title: 'Files open safely',
        body: 'Pictures, sound, video and PDFs open in place as before. Any other file downloads instead of opening in the browser.',
      },
      {
        area: 'app',
        title: 'Limits nobody working will meet',
        body: 'Sending, inviting, uploading, searching and the like now have limits far above what a person does, so a script or a stolen session cannot flood everybody. If you ever hit one, TeamUp says how long to wait.',
      },
      {
        area: 'chat',
        title: 'Links say where they really go',
        body: 'A link whose words look like one address but which leads to another now shows the real address beside it, so a message cannot dress one site up as another.',
      },
      {
        area: 'app',
        title: 'Signing out leaves nothing behind',
        body: 'However you are signed out, by choice, by a password change or because your session ended, TeamUp clears what it kept on that device. Sharing into TeamUp from another website is refused, and anything shared in a link says it came from outside.',
      },
    ],
  },
  {
    id: '2026-11-12-e',
    date: '2026-11-12',
    title: 'Formatted messages, shared in and out',
    changes: [
      {
        area: 'chat',
        title: 'Format what you write',
        body: 'Shift+Enter starts a new line. Write *bold*, _italic_, ~struck~ or `code`, start lines with - or 1. for a list or > for a quote, or use the T button by the message box. Ctrl+B and Ctrl+I work too.',
        link: '/chat',
      },
      {
        area: 'chat',
        title: 'Pasting keeps the formatting',
        body: 'Text pasted from Google Docs, Word, an email or a web page keeps its bold, italics, lists and links.',
      },
      {
        area: 'chat',
        title: 'Copy or share a message',
        body: 'Copy text in a message’s menu copies it with its formatting, ready for a document, an email, WhatsApp or Slack. On a phone, Share to another app opens the share sheet.',
      },
      {
        area: 'meetings',
        title: 'Share the minutes',
        body: 'On a meeting’s minutes, Send to a conversation posts them in TeamUp as a formatted message, Copy takes them to a document or another app, and Share opens your phone’s share sheet.',
        link: '/meeting-notes',
      },
      {
        area: 'app',
        title: 'Share into TeamUp from other apps',
        body: 'With TeamUp installed on your phone, Share in another app offers TeamUp. Choose one or more conversations, or somebody for a new chat, and it goes there with its photos and files.',
      },
    ],
  },
  {
    id: '2026-11-12-d',
    date: '2026-11-12',
    title: 'See who is typing',
    changes: [
      {
        area: 'chat',
        title: 'Who is typing',
        body: 'While somebody writes in a conversation you have open, a line under the message box says so, such as “Alice is typing…”. It goes when they send, clear what they wrote, or stop for a few seconds.',
        link: '/chat',
      },
    ],
  },
  {
    id: '2026-11-12-c',
    date: '2026-11-12',
    title: 'A call window you can resize',
    changes: [
      {
        area: 'meetings',
        title: 'Make the minimised call bigger or smaller',
        body: 'Drag the arrow in the top left corner of a minimised call to resize it; the people in it grow and shrink with it, and it keeps that size next time on that device.',
      },
      {
        area: 'meetings',
        title: 'Move it with a finger',
        body: 'On a phone, put a finger anywhere on the minimised call and drag it where you want it. It stays on the screen, even when you turn the phone.',
      },
    ],
  },
  {
    id: '2026-11-12-b',
    date: '2026-11-12',
    title: 'Meeting chat in bubbles, and two fixes',
    changes: [
      {
        area: 'meetings',
        title: 'Every message in its own bubble',
        body: 'Messages in a meeting’s chat each sit in their own bubble with the time they were sent, so several in a row from one person are easy to tell apart. Yours are on the right.',
        link: '/calls',
      },
      {
        area: 'meetings',
        title: 'A phone stops ringing for good',
        body: 'When a caller hangs up just as your phone was about to ring again, it no longer starts ringing a moment later for a call that is over.',
      },
      {
        area: 'chat',
        title: 'Edit puts you straight in the box',
        body: 'Choosing Edit on one of your messages puts the cursor in the edit box, so you can start typing at once.',
      },
    ],
  },
  {
    id: '2026-11-12',
    date: '2026-11-12',
    title: 'Chat inside meetings, and tidier tasks',
    changes: [
      {
        area: 'meetings',
        title: 'Chat inside every meeting',
        body: 'The speech-bubble button in a call opens a chat for everyone in it, guests included. Somebody who joins late reads what came before, and a new message shows over the video when the chat is closed. Afterwards the chat stays in the call log, and the minutes include it.',
        link: '/calls',
      },
      {
        area: 'tasks',
        title: 'Labels you can make',
        body: 'Type a name in a task’s Labels card, such as bug or frontend, and press Enter. Labels your organization already has are offered as you type, and the Board can show only the tasks with one label.',
        link: '/tasks?tab=board',
      },
      {
        area: 'tasks',
        title: 'Long tasks stay short',
        body: 'A task’s history, comments, pull requests and commits show the latest few, with Show more for the rest.',
      },
      {
        area: 'app',
        title: 'What’s new',
        body: 'This page. It opens by itself once after each update, and you can find it again from your picture in the top corner.',
      },
    ],
  },
  {
    id: '2026-11-11',
    date: '2026-11-11',
    title: 'Task news, busy callers and meeting minutes',
    changes: [
      {
        area: 'tasks',
        title: 'Hear about your tasks',
        body: 'Whoever a task is assigned to, whoever made it and anybody added to it are told, with a sound, when it changes or somebody comments. Tasks shows a count, and each task card a dot, until you have looked.',
        link: '/tasks',
      },
      {
        area: 'calendar',
        title: 'Tasks on the calendar',
        body: 'Tasks with a due date show on that day in your calendar.',
        link: '/calendar',
      },
      {
        area: 'meetings',
        title: 'Calling somebody who is busy',
        body: 'Somebody already in a call or meeting is not rung; you are told they are busy. Missed and declined calls have their own marks in your calls list.',
      },
      {
        area: 'meetings',
        title: 'Meeting minutes',
        body: 'The note-taker starts once two people are in a meeting together, and the minutes are written reliably again.',
        link: '/meeting-notes',
      },
      {
        area: 'code',
        title: 'Tasks follow pull requests',
        body: 'A new pull request for a finished task moves it back to In progress, and merging it moves it to Done again.',
      },
      {
        area: 'chat',
        title: 'Replies and files',
        body: 'Replies in a thread show two ticks once read, and files on a task’s comments download properly.',
      },
    ],
  },
  {
    id: '2026-11-10',
    date: '2026-11-10',
    title: 'Projects, GitHub and Bitbucket',
    changes: [
      {
        area: 'code',
        title: 'Projects with their code',
        body: 'A project can be linked to GitHub or Bitbucket repositories. Each task gets a key such as TEA-2, and a branch, commit or pull request that mentions it shows on the task and moves it along the board.',
        link: '/projects',
      },
      {
        area: 'calendar',
        title: 'Google Calendar',
        body: 'Connect your Google Calendar from the Calendar page to see both side by side.',
        link: '/calendar',
      },
      {
        area: 'tasks',
        title: 'Mentions and pictures',
        body: 'Mention people in a task’s comments with @, and everybody’s picture shows wherever their name does.',
      },
    ],
  },
];

/** The newest entry: what somebody has to have seen for What's new to stay closed. */
export const LATEST = RELEASES[0].id;

/** The entries somebody has not seen, newest first: all of them for somebody who has seen none. */
export function unseen(seen: string | null | undefined): Release[] {
  if (!seen) return RELEASES.slice(0, 1);
  return RELEASES.filter((r) => r.id > seen);
}
