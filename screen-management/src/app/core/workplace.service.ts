import { inject, Injectable } from '@angular/core';
import { HttpClient, HttpContext } from '@angular/common/http';
import { Observable } from 'rxjs';
import { SILENT } from './loading.service';

// ---------- Desktops: GET/DELETE /api/devices ----------

export interface Desktop {
  id: number;
  name: string;
  os: string;              // linux/amd64
  platform: string;        // linux-amd64
  agentVersion: string;
  user: { id: number; name: string; email: string; team: string };
  createdAt: string;
  lastSeenAt: string | null;
  revokedAt: string | null;
  online: boolean;
}
export interface DesktopList { latest: Record<string, string>; items: Desktop[]; }

// ---------- Emails: /api/email-reports ----------

export type ReportKind = 'daily_summary' | 'attendance' | 'productivity' | 'apps_websites';
export type Frequency = 'daily' | 'weekly' | 'monthly';

export interface EmailReportInput {
  name: string;
  report: ReportKind;
  frequency: Frequency;
  sendTime: string;        // HH:MM
  weekday: number | null;  // weekly: 0 = Sunday
  monthDay: number | null; // monthly: 1–28
  timeZone: string;
  teamId: number | null;   // null: everyone
  recipients: string[];
  enabled: boolean;
}
export interface EmailReport extends EmailReportInput {
  id: number;
  teamName: string;
  createdBy: string;
  createdAt: string;
  lastSentAt: string | null;
}
export interface EmailReportList { items: EmailReport[]; mailConfigured: boolean; }

// ---------- Time Tracker days: GET /api/timetracker/days ----------

export interface WorkDay {
  userId: number; date: string;
  inTime: number; finish: number;   // seconds after midnight
  signedIn: boolean;
  working: number; productive: number; nonProductive: number; neutral: number; idle: number;
  apps: { name: string; seconds: number }[];
}

/** Admin pages without a service of their own: Desktops, Emails, Office TV */
@Injectable({ providedIn: 'root' })
export class WorkplaceService {
  private readonly http = inject(HttpClient);

  desktops(status: 'active' | 'removed', quiet = false): Observable<DesktopList> {
    return this.http.get<DesktopList>('/api/devices', { params: { status }, context: quiet ? silent() : undefined });
  }

  removeDesktop(id: number) {
    return this.http.delete<void>(`/api/devices/${id}`);
  }

  emailReports(): Observable<EmailReportList> {
    return this.http.get<EmailReportList>('/api/email-reports');
  }

  saveEmailReport(id: number | null, body: EmailReportInput): Observable<EmailReport> {
    return id === null
      ? this.http.post<EmailReport>('/api/email-reports', body)
      : this.http.put<EmailReport>(`/api/email-reports/${id}`, body);
  }

  setEmailReportEnabled(id: number, enabled: boolean): Observable<EmailReport> {
    return this.http.put<EmailReport>(`/api/email-reports/${id}/enabled`, { enabled });
  }

  deleteEmailReport(id: number) {
    return this.http.delete<void>(`/api/email-reports/${id}`);
  }

  days(q: { from: string; to: string; tz: string; users?: number[] }, quiet = false): Observable<WorkDay[]> {
    const params: Record<string, string> = { from: q.from, to: q.to, tz: q.tz };
    if (q.users?.length) params['users'] = q.users.join(',');
    return this.http.get<WorkDay[]>('/api/timetracker/days', { params, context: quiet ? silent() : undefined });
  }
}

/** Auto-refreshing screens poll; that should not flash the progress bar */
export function silent() {
  return new HttpContext().set(SILENT, true);
}

/** Today's date (YYYY-MM-DD) and seconds since midnight in a time zone */
export function nowIn(tz: string, at = new Date()) {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-CA', {
    timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(at).map(p => [p.type, p.value]));
  return {
    date: `${parts['year']}-${parts['month']}-${parts['day']}`,
    seconds: Number(parts['hour']) * 3600 + Number(parts['minute']) * 60 + Number(parts['second']),
  };
}

/** 2h 05m, 45m, 30s */
export function hm(seconds: number) {
  const h = Math.floor(seconds / 3600), m = Math.floor((seconds % 3600) / 60);
  return h ? `${h}h ${String(m).padStart(2, '0')}m` : m ? `${m}m` : seconds > 0 ? `${Math.round(seconds)}s` : '0m';
}

/** 09:05 from seconds after midnight */
export function clock(seconds: number) {
  const h = Math.floor(seconds / 3600) % 24, m = Math.floor((seconds % 3600) / 60);
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`;
}

/** "3 minutes ago" */
export function ago(iso: string | null, now = Date.now()) {
  if (!iso) return 'Never';
  const s = Math.max(1, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 45) return 'Just now';
  const units: [number, string][] = [[86400, 'day'], [3600, 'hour'], [60, 'minute']];
  const [size, name] = units.find(([u]) => s >= u) ?? [60, 'minute'];
  const n = Math.max(1, Math.floor(s / size));
  return `${n} ${name}${n > 1 ? 's' : ''} ago`;
}

export function initials(name: string) {
  return name.split(/\s+/).filter(Boolean).slice(0, 2).map(p => p[0].toUpperCase()).join('') || '?';
}

const AVATAR_COLORS = ['#4f46e5', '#0ea5e9', '#f97316', '#14b8a6', '#a855f7', '#ec4899', '#22c55e', '#64748b'];
export function avatarColor(id: number) {
  return AVATAR_COLORS[Math.abs(id) % AVATAR_COLORS.length];
}

/** Compares dotted versions: 0.10.0 > 0.9.3 */
export function newerVersion(a: string, b: string) {
  const pa = a.split('.').map(Number), pb = b.split('.').map(Number);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] ?? 0, y = pb[i] ?? 0;
    if (x !== y) return x > y;
  }
  return false;
}
