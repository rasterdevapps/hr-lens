import { inject, Injectable } from '@angular/core';
import { HttpClient, HttpContext, HttpParams, HttpResponse } from '@angular/common/http';
import { Observable } from 'rxjs';
import { SILENT } from './loading.service';

export interface Capture {
  id: number;
  userId: number;
  userName: string;
  kind: 'screenshot' | 'recording';
  startedAt: string;
  seconds: number;       // recordings
  app: string;           // front application when taken
  title: string;
  bytes: number;
  device: string;
}

/** Key presses and mouse clicks in one 10-minute bucket */
export interface InputBucket { start: number; keys: number; clicks: number; }   // start: seconds after local midnight

export interface LiveStatus {
  userId: number;
  online: boolean;       // the agent sent a heartbeat recently
  device: string;
  lastSeen: string | null;
  frameAt: string | null;
  noScreen: boolean;     // that computer's agent cannot capture the screen (Windows, macOS so far)
}

/** Screenshots, recordings and live view sent by the desktop agent — /api/monitoring/* */
@Injectable({ providedIn: 'root' })
export class MonitoringService {
  private readonly http = inject(HttpClient);

  captures(kind: Capture['kind'], q: { users: number[]; from: string; to: string; tz: string }): Observable<Capture[]> {
    let params = new HttpParams().set('kind', kind).set('from', q.from).set('to', q.to).set('tz', q.tz);
    if (q.users.length) params = params.set('users', q.users.join(','));
    return this.http.get<Capture[]>('/api/monitoring/captures', { params });
  }

  input(q: { users: number[]; from: string; to: string; tz: string }): Observable<InputBucket[]> {
    let params = new HttpParams().set('from', q.from).set('to', q.to).set('tz', q.tz);
    if (q.users.length) params = params.set('users', q.users.join(','));
    return this.http.get<InputBucket[]>('/api/monitoring/input', { params });
  }

  /** Same-origin URL: the session cookie authorises <img> and <video> */
  fileUrl(id: number) {
    return `/api/monitoring/captures/${id}/file`;
  }

  /** Who can be watched; asking also tells their agents to start sending live frames */
  live(users: number[]): Observable<LiveStatus[]> {
    return this.http.get<LiveStatus[]>('/api/monitoring/live', { params: { users: users.join(',') }, context: silent() });
  }

  /** Newest live frame; 204 while waiting for the first one */
  frame(userId: number): Observable<HttpResponse<Blob>> {
    return this.http.get(`/api/monitoring/live/${userId}/frame`, { responseType: 'blob', observe: 'response', context: silent() });
  }
}

/** Live Screen polls every few seconds; that should not flash the progress bar */
function silent() {
  return new HttpContext().set(SILENT, true);
}
