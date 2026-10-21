import { inject, Injectable } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { Observable } from 'rxjs';

export type Productivity = 'productive' | 'neutral' | 'unproductive';
export type ActivityGroup = 'website' | 'application' | 'category';

/** Filters shared by every activity request */
export interface ActivityQuery {
  users: number[];
  from: string;            // YYYY-MM-DD, inclusive
  to: string;
  tz: string;              // IANA zone the days are counted in
  productivity: Productivity | '';
  search: string;
}

export interface ActivityItem {
  name: string;
  detail: string;          // browser for a website, process for an app, app count for a category
  category: string;
  productivity: Productivity;
  seconds: number;
  percent: number;
}

export interface ActivityTotals { seconds: number; productive: number; neutral: number; unproductive: number; }

export interface ActivitySummary { totals: ActivityTotals; items: ActivityItem[]; }

export interface ActivityEvent {
  id: number;
  userId: number;
  userName: string;
  startedAt: string;
  seconds: number;
  app: string;
  appName: string;
  title: string;
  domain: string | null;
  category: string;
  productivity: Productivity;
}

export interface ActivityEvents { total: number; items: ActivityEvent[]; }

/** Websites, applications and categories people used — GET /api/activity/* */
@Injectable({ providedIn: 'root' })
export class ActivityService {
  private readonly http = inject(HttpClient);

  summary(group: ActivityGroup, q: ActivityQuery): Observable<ActivitySummary> {
    return this.http.get<ActivitySummary>('/api/activity/summary', { params: params(q).set('group', group) });
  }

  events(q: ActivityQuery, page: number, pageSize: number, dir: 'asc' | 'desc'): Observable<ActivityEvents> {
    return this.http.get<ActivityEvents>('/api/activity/events', {
      params: params(q).set('page', page).set('pageSize', pageSize).set('dir', dir),
    });
  }
}

function params(q: ActivityQuery) {
  let p = new HttpParams().set('from', q.from).set('to', q.to).set('tz', q.tz);
  if (q.users.length) p = p.set('users', q.users.join(','));
  if (q.productivity) p = p.set('productivity', q.productivity);
  if (q.search) p = p.set('search', q.search);
  return p;
}
