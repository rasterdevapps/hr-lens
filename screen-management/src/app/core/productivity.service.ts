import { inject, Injectable } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { Observable } from 'rxjs';
import { Productivity } from './activity.service';

export interface LevelTotals { working: number; productive: number; neutral: number; unproductive: number; away: number; }

export interface BreakdownRow extends LevelTotals { from: string; to: string; expected: number; }

export interface TimeSegment { start: number; end: number; productivity: Productivity; }   // seconds after midnight

export interface AppTime { name: string; seconds: number; percent: number; }

export interface DayTimeline { date: string; segments: TimeSegment[]; }

/** One Productivity Bar: seconds at each level out of capacity */
export interface ProductivityBar { start: number; date?: string; productive: number; neutral: number; unproductive: number; capacity: number; }

export interface CategoryTime { name: string; seconds: number; percent: number; }

export interface Trends {
  totals: LevelTotals;
  days: DayTimeline[] | null;        // one person, newest first; a single day shows its week
  bars: { unit: 'slot' | 'day'; slot: number; items: ProductivityBar[] };
  breakdown: BreakdownRow[];         // last day, day before, week to date
  categories: { period: CategoryTime[]; breakdown: { name: string; cells: CategoryTime[] }[] };
  applications: Record<Productivity, AppTime[]>;
}

export type RuleKind = 'application' | 'website';
export type RuleSort = 'time' | 'hits' | 'name';

export interface Rule {
  id: number;
  kind: RuleKind;
  name: string;
  detail: string;
  browser: boolean;
  category: string;
  productivity: Productivity;
  hits: number;
  seconds: number;
  updatedAt: string;
}

export interface RuleList { total: number; page: number; pageSize: number; categories: string[]; items: Rule[]; }

export interface RuleQuery {
  kind: RuleKind;
  search: string;
  productivity: Productivity | '';
  category: string;
  sort: RuleSort;
  page: number;      // from 1
  pageSize: number;
}

/** Trends and app ratings — GET/POST/PUT /api/productivity/* */
@Injectable({ providedIn: 'root' })
export class ProductivityService {
  private readonly http = inject(HttpClient);

  trends(q: { users: number[]; from: string; to: string; tz: string }): Observable<Trends> {
    let params = new HttpParams().set('from', q.from).set('to', q.to).set('tz', q.tz);
    if (q.users.length) params = params.set('users', q.users.join(','));
    return this.http.get<Trends>('/api/productivity/trends', { params });
  }

  rules(q: RuleQuery): Observable<RuleList> {
    let params = new HttpParams().set('kind', q.kind).set('sort', q.sort).set('page', q.page).set('pageSize', q.pageSize);
    if (q.search) params = params.set('search', q.search);
    if (q.productivity) params = params.set('productivity', q.productivity);
    if (q.category) params = params.set('category', q.category);
    return this.http.get<RuleList>('/api/productivity/rules', { params });
  }

  createRule(rule: { kind: RuleKind; name: string; category: string; productivity: Productivity }) {
    return this.http.post<{ id: number }>('/api/productivity/rules', rule);
  }

  updateRule(id: number, change: { category: string; productivity: Productivity }) {
    return this.http.put<{ updated: number }>(`/api/productivity/rules/${id}`, change);
  }

  /** Rates several rules at once; category is left alone unless given */
  updateRules(ids: number[], productivity: Productivity, category?: string) {
    return this.http.put<{ updated: number }>('/api/productivity/rules', { ids, productivity, ...(category ? { category } : {}) });
  }
}
