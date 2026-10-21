import { Component, computed, HostListener, inject, signal } from '@angular/core';
import { DatePipe } from '@angular/common';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { firstValueFrom } from 'rxjs';
import { ApiError, Team } from '../../core/api.types';
import { AuthService, errorMessage } from '../../core/auth.service';
import {
  avatarColor, clock, EmailReport, EmailReportInput, Frequency, hm, initials, nowIn, ReportKind, WorkDay, WorkplaceService,
} from '../../core/workplace.service';
import { TimeTrackerService } from '../dashboard/time-tracker.service';
import * as check from '../shared/validation';
import { Errors, FormErrors } from '../shared/validation';

const TZ = 'Asia/Kolkata';
const MAX_RECIPIENTS = 20;

export const REPORTS: { key: ReportKind; label: string; icon: string; hint: string }[] = [
  { key: 'daily_summary', label: 'Daily summary', icon: 'summarize', hint: 'Hours, presence, productivity and top apps' },
  { key: 'attendance', label: 'Attendance', icon: 'event_available', hint: 'Who came in, start times and hours' },
  { key: 'productivity', label: 'Productivity', icon: 'insights', hint: 'Productive, neutral and unproductive time per person' },
  { key: 'apps_websites', label: 'Apps & websites', icon: 'apps', hint: 'Where the time went' },
];
const FREQUENCIES: { key: Frequency; label: string }[] = [
  { key: 'daily', label: 'Daily' }, { key: 'weekly', label: 'Weekly' }, { key: 'monthly', label: 'Monthly' },
];
const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

interface Draft extends EmailReportInput { id: number | null; }

/** Emails: report emails sent on a schedule */
@Component({
  selector: 'app-emails',
  standalone: true,
  imports: [DatePipe, MatIconModule, MatTooltipModule],
  templateUrl: './emails.component.html',
  styleUrl: './emails.component.scss',
})
export class EmailsComponent {
  private readonly api = inject(WorkplaceService);
  private readonly http = inject(HttpClient);
  private readonly auth = inject(AuthService);
  private readonly people = inject(TimeTrackerService);

  readonly reports = REPORTS;
  readonly frequencies = FREQUENCIES;
  readonly weekdays = WEEKDAYS;
  readonly monthDays = Array.from({ length: 28 }, (_, i) => i + 1);

  items = signal<EmailReport[]>([]);
  mailConfigured = signal(false);
  loading = signal(true);
  error = signal('');
  notice = signal('');
  selectedId = signal<number | null>(null);
  teams = signal<Team[]>([]);

  readonly selected = computed(() => this.items().find(r => r.id === this.selectedId()) ?? this.items().at(0) ?? null);
  readonly stats = computed(() => {
    const list = this.items();
    return {
      total: list.length,
      enabled: list.filter(r => r.enabled).length,
      recipients: new Set(list.flatMap(r => r.recipients)).size,
    };
  });

  constructor() {
    this.load();
  }

  async load() {
    this.loading.set(true);
    try {
      const res = await firstValueFrom(this.api.emailReports());
      this.items.set(res.items);
      this.mailConfigured.set(res.mailConfigured);
      this.error.set('');
      this.loadPreview();
    } catch (err) {
      this.error.set(errorMessage(err, 'Could not load report emails.'));
    } finally {
      this.loading.set(false);
    }
  }

  select(r: EmailReport) {
    this.selectedId.set(r.id);
    this.loadPreview();
  }

  reportOf(key: ReportKind) { return REPORTS.find(r => r.key === key)!; }

  scheduleLabel(r: EmailReportInput) {
    const at = r.sendTime;
    if (r.frequency === 'weekly') return `Every ${WEEKDAYS[r.weekday ?? 1]} at ${at}`;
    if (r.frequency === 'monthly') return `Monthly on day ${r.monthDay ?? 1} at ${at}`;
    return `Every day at ${at}`;
  }

  /** When it would go out next, in its time zone */
  nextRun(r: EmailReportInput) {
    if (!r.enabled) return 'Paused';
    const now = nowIn(r.timeZone || TZ);
    const [h, m] = r.sendTime.split(':').map(Number);
    const sendAt = h * 3600 + m * 60;
    const start = new Date(`${now.date}T00:00:00Z`);
    for (let i = 0; i < 62; i++) {
      const d = new Date(start.getTime() + i * 86_400_000);
      const fits = r.frequency === 'daily' || (r.frequency === 'weekly' && d.getUTCDay() === r.weekday)
        || (r.frequency === 'monthly' && d.getUTCDate() === r.monthDay);
      if (!fits || (i === 0 && sendAt <= now.seconds)) continue;
      const day = i === 0 ? 'Today' : i === 1 ? 'Tomorrow'
        : new Intl.DateTimeFormat('en-GB', { timeZone: 'UTC', weekday: 'short', day: 'numeric', month: 'short' }).format(d);
      return `${day} at ${r.sendTime}`;
    }
    return '–';
  }

  async toggle(r: EmailReport) {
    try {
      const updated = await firstValueFrom(this.api.setEmailReportEnabled(r.id, !r.enabled));
      this.items.update(list => list.map(x => (x.id === r.id ? updated : x)));
      this.notice.set(`${r.name} ${updated.enabled ? 'resumed' : 'paused'}.`);
    } catch (err) {
      this.notice.set(errorMessage(err));
    }
  }

  // ---------- Preview: what the email would contain, from real data ----------
  preview = signal<{ loading: boolean; error: string; days: WorkDay[]; from: string; to: string; people: number[] }>(
    { loading: false, error: '', days: [], from: '', to: '', people: [] });

  /** Period the report covers: yesterday, the 7 days or 30 days before today */
  private period(f: Frequency, tz: string) {
    const today = new Date(`${nowIn(tz).date}T00:00:00Z`);
    const day = (n: number) => new Date(today.getTime() - n * 86_400_000).toISOString().slice(0, 10);
    return f === 'daily' ? { from: day(1), to: day(1) } : f === 'weekly' ? { from: day(7), to: day(1) } : { from: day(30), to: day(1) };
  }

  private teamMembers(teamId: number | null) {
    const all = this.people.employees;
    return teamId !== null ? all.filter(e => e.teamId === teamId).map(e => e.id) : all.map(e => e.id);
  }

  async loadPreview() {
    const r = this.selected();
    if (!r) return;
    const { from, to } = this.period(r.frequency, r.timeZone || TZ);
    const people = this.teamMembers(r.teamId);
    this.preview.set({ loading: true, error: '', days: [], from, to, people });
    try {
      const days = people.length ? await firstValueFrom(this.api.days({ from, to, tz: r.timeZone || TZ, users: people }, true)) : [];
      if (this.selected()?.id === r.id) this.preview.set({ loading: false, error: '', days, from, to, people });
    } catch (err) {
      if (this.selected()?.id !== r.id) return;   // an older preview request: the newer one is showing
      this.preview.set({ loading: false, error: errorMessage(err, 'Could not build the preview.'), days: [], from, to, people });
    }
  }

  readonly digest = computed(() => {
    const p = this.preview();
    const names = new Map(this.people.employees.map(e => [e.id, e.name]));
    const per = new Map<number, { id: number; name: string; days: number; working: number; productive: number; neutral: number; unproductive: number; inTimes: number[] }>();
    for (const id of p.people) per.set(id, { id, name: names.get(id) ?? `#${id}`, days: 0, working: 0, productive: 0, neutral: 0, unproductive: 0, inTimes: [] });
    const apps = new Map<string, number>();
    for (const d of p.days) {
      const row = per.get(d.userId);
      if (!row) continue;
      row.days++;
      row.working += d.working;
      row.productive += d.productive;
      row.neutral += d.neutral;
      row.unproductive += d.nonProductive;
      row.inTimes.push(d.inTime);
      for (const a of d.apps) apps.set(a.name, (apps.get(a.name) ?? 0) + a.seconds);
    }
    const rows = [...per.values()].map(r => ({
      ...r,
      hours: hm(r.working),
      avgIn: r.inTimes.length ? clock(r.inTimes.reduce((a, b) => a + b, 0) / r.inTimes.length) : '–',
      score: r.working ? Math.round((r.productive / r.working) * 100) : 0,
      initials: initials(r.name), color: avatarColor(r.id),
    })).sort((a, b) => b.working - a.working || a.name.localeCompare(b.name));
    const total = rows.reduce((s, r) => s + r.working, 0);
    const sum = (k: 'productive' | 'neutral' | 'unproductive') => rows.reduce((s, r) => s + r[k], 0);
    const present = rows.filter(r => r.days > 0);
    const appList = [...apps].sort((a, b) => b[1] - a[1]).slice(0, 8);
    const topApp = appList[0]?.[1] ?? 1;
    return {
      rows, present: present.length, people: rows.length,
      total: hm(total), average: present.length ? hm(total / present.length) : '0m',
      split: total ? {
        productive: (sum('productive') / total) * 100, neutral: (sum('neutral') / total) * 100, unproductive: (sum('unproductive') / total) * 100,
      } : null,
      score: total ? Math.round((sum('productive') / total) * 100) : 0,
      apps: appList.map(([name, s]) => ({ name, label: hm(s), width: (s / topApp) * 100 })),
    };
  });

  periodLabel() {
    const p = this.preview();
    if (!p.from) return '';
    const f = (s: string) => new Intl.DateTimeFormat('en-GB', { timeZone: 'UTC', day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(`${s}T00:00:00Z`));
    return p.from === p.to ? f(p.from) : `${f(p.from)} – ${f(p.to)}`;
  }

  // ---------- Add / edit ----------
  draft = signal<Draft | null>(null);
  recipientText = signal('');
  saving = signal(false);
  formError = signal('');
  readonly form = new FormErrors(() => this.checkDraft());
  readonly errors = this.form.shown;

  private checkDraft(): Errors {
    const d = this.draft();
    if (!d) return {};
    const typed = this.recipientText().trim();
    let recipients = '';
    if (typed) recipients = check.email(typed) || (d.recipients.includes(typed.toLowerCase()) ? `${typed} is already added` : '');
    else if (!d.recipients.length) recipients = 'Add at least one recipient';
    if (!recipients && d.recipients.length + (typed ? 1 : 0) > MAX_RECIPIENTS) recipients = `Use at most ${MAX_RECIPIENTS} recipients`;
    return {
      name: check.requiredText(d.name, 100, 'Name'),
      sendTime: /^([01]\d|2[0-3]):[0-5]\d$/.test(d.sendTime) ? '' : 'Enter a time such as 09:00',
      weekday: d.frequency === 'weekly' && d.weekday === null ? 'Choose a day of the week' : '',
      monthDay: d.frequency === 'monthly' && !d.monthDay ? 'Choose a day from 1 to 28' : '',
      recipients,
    };
  }

  async add() {
    await this.open({
      id: null, name: '', report: 'daily_summary', frequency: 'daily', sendTime: '09:00', weekday: 1, monthDay: 1,
      timeZone: TZ, teamId: null, recipients: [this.auth.user()?.email ?? ''].filter(Boolean), enabled: true,
    });
  }

  async edit(r: EmailReport) {
    await this.open({
      id: r.id, name: r.name, report: r.report, frequency: r.frequency, sendTime: r.sendTime,
      weekday: r.weekday ?? 1, monthDay: r.monthDay ?? 1, timeZone: r.timeZone, teamId: r.teamId,
      recipients: [...r.recipients], enabled: r.enabled,
    });
  }

  private async open(d: Draft) {
    this.form.reset();
    this.formError.set('');
    this.recipientText.set('');
    try {
      this.teams.set(await firstValueFrom(this.http.get<Team[]>('/api/teams', { params: { status: 'active' } })));
    } catch {
      this.teams.set([]);   // the form still works for everyone
    }
    this.draft.set(d);
  }

  patch(change: Partial<Draft>, field?: string) {
    this.draft.update(d => (d ? { ...d, ...change } : d));
    this.formError.set('');
    if (field) this.form.touch(field);
    else this.form.refresh();
  }

  /** Default name from the report and how often it goes out */
  suggestName() {
    const d = this.draft();
    if (!d || d.name.trim()) return;
    const freq = FREQUENCIES.find(f => f.key === d.frequency)!.label;
    this.patch({ name: `${freq} ${this.reportOf(d.report).label.toLowerCase()}` });
  }

  typeRecipient(value: string) {
    // A comma, semicolon or space ends an address
    const parts = value.split(/[,;\s]+/);
    const last = parts.pop() ?? '';
    for (const p of parts) this.addRecipient(p);
    this.recipientText.set(last);
    this.form.touch('recipients');
  }

  addRecipient(raw = this.recipientText()) {
    const d = this.draft();
    const addr = raw.trim().toLowerCase();
    if (!d || !addr) return true;
    if (check.email(addr) || d.recipients.includes(addr) || d.recipients.length >= MAX_RECIPIENTS) {
      this.recipientText.set(raw.trim());
      this.form.touch('recipients');
      return false;
    }
    this.draft.set({ ...d, recipients: [...d.recipients, addr] });
    if (raw === this.recipientText()) this.recipientText.set('');
    this.form.touch('recipients');
    return true;
  }

  removeRecipient(addr: string) {
    const d = this.draft();
    if (!d) return;
    this.draft.set({ ...d, recipients: d.recipients.filter(a => a !== addr) });
    this.form.touch('recipients');
  }

  recipientKey(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault();
      this.addRecipient();
    } else if (e.key === 'Backspace' && !this.recipientText()) {
      const d = this.draft();
      if (d?.recipients.length) this.removeRecipient(d.recipients[d.recipients.length - 1]);
    }
  }

  async save() {
    const d = this.draft();
    if (!d || this.saving()) return;
    this.formError.set('');
    if (this.recipientText().trim() && !this.addRecipient()) {
      this.form.submit();
      return;
    }
    if (!this.form.submit()) return;
    const { id, ...body } = this.draft()!;
    this.saving.set(true);
    try {
      const saved = await firstValueFrom(this.api.saveEmailReport(id, body));
      this.draft.set(null);
      this.notice.set(id === null ? `${saved.name} scheduled.` : 'Changes saved.');
      this.selectedId.set(saved.id);
      await this.load();
    } catch (err) {
      const api = err instanceof HttpErrorResponse ? (err.error as ApiError | null) : null;
      if (api?.fields) this.form.setServer(api.fields);
      else this.formError.set(errorMessage(err));
    } finally {
      this.saving.set(false);
    }
  }

  // ---------- Delete ----------
  deleting = signal<EmailReport | null>(null);
  deleteError = signal('');

  async confirmDelete() {
    const r = this.deleting();
    if (!r || this.saving()) return;
    this.saving.set(true);
    try {
      await firstValueFrom(this.api.deleteEmailReport(r.id));
      this.deleting.set(null);
      this.notice.set(`${r.name} deleted.`);
      if (this.selectedId() === r.id) this.selectedId.set(null);
      await this.load();
    } catch (err) {
      this.deleteError.set(errorMessage(err));
    } finally {
      this.saving.set(false);
    }
  }

  @HostListener('document:keydown.escape')
  onEscape() {
    this.draft.set(null);
    this.deleting.set(null);
  }
}
