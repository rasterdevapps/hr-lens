import { pipeOffset } from '../shared/period';
import { Component, computed, inject, input, signal } from '@angular/core';
import { DatePipe, formatDate, NgTemplateOutlet } from '@angular/common';
import { MatIconModule } from '@angular/material/icon';
import { Employee } from '../dashboard/time-tracker.service';
import { Capture, MonitoringService } from '../../core/monitoring.service';
import { loadOnChange } from '../shared/load';
import { isoDate } from '../shared/period';
import type { Zone } from './screenshots.component';

interface Recording {
  id: number;
  userId: number;
  name: string;
  initials: string;
  color: string;
  url: string;
  time: string;       // 9:05 AM
  until: string;      // 9:15 AM
  duration: string;   // 10 min
  app: string;
}

interface HourGroup { label: string; items: Recording[]; }

const AVATARS = ['#4f46e5', '#0ea5e9', '#f97316', '#14b8a6', '#a855f7', '#ec4899', '#22c55e', '#64748b'];

@Component({
  selector: 'app-screen-recordings',
  standalone: true,
  imports: [DatePipe, NgTemplateOutlet, MatIconModule],
  templateUrl: './screen-recordings.component.html',
  styleUrl: './screen-recordings.component.scss',
})
export class ScreenRecordingsComponent {
  private readonly api = inject(MonitoringService);

  members = input.required<Employee[]>();
  date = input.required<Date>();
  zone = input.required<Zone>();

  playing = signal<Recording | null>(null);
  private attempt = signal(0);

  private readonly request = computed(() => ({
    users: this.members().map(m => m.id), from: isoDate(this.date()), to: isoDate(this.date()),
    tz: this.zone().tz, attempt: this.attempt(),
  }));
  readonly list = loadOnChange(this.request, r => this.api.captures('recording', r));

  retry() { this.attempt.update(n => n + 1); }

  /** Oldest first, so playing on runs forward through the day */
  recordings = computed<Recording[]>(() => {
    const tz = this.zone().tz;
    return (this.list().data ?? []).map((c: Capture) => {
      const end = new Date(new Date(c.startedAt).getTime() + c.seconds * 1000);
      const pipe = pipeOffset(tz, new Date(c.startedAt));
      return {
        id: c.id, userId: c.userId, name: c.userName,
        initials: c.userName.split(' ').map(p => p[0]).join('').slice(0, 2).toUpperCase(),
        color: AVATARS[c.userId % AVATARS.length],
        url: this.api.fileUrl(c.id),
        time: formatDate(c.startedAt, 'h:mm a', 'en-US', pipe),
        until: formatDate(end, 'h:mm a', 'en-US', pipe),
        duration: c.seconds >= 60 ? `${Math.round(c.seconds / 60)} min` : `${c.seconds} s`,
        app: c.app,
      };
    });
  });

  groups = computed<HourGroup[]>(() => {
    const tz = this.zone().tz;
    const groups: HourGroup[] = [];
    const data = this.list().data ?? [];
    this.recordings().forEach((r, i) => {
      const hour = Number(formatDate(data[i].startedAt, 'H', 'en-US', pipeOffset(tz, new Date(data[i].startedAt))));
      const label = `${hour % 12 || 12} ${hour < 12 ? 'AM' : 'PM'}`;
      let g = groups[groups.length - 1];
      if (g?.label !== label) groups.push(g = { label, items: [] });
      g.items.push(r);
    });
    return groups;
  });

  /** The same person's next or previous piece */
  private neighbour(step: number) {
    const p = this.playing();
    if (!p) return null;
    const mine = this.recordings().filter(r => r.userId === p.userId);
    return mine[mine.findIndex(r => r.id === p.id) + step] ?? null;
  }
  hasNext = computed(() => !!this.neighbour(1));
  hasPrev = computed(() => !!this.neighbour(-1));

  next() { const n = this.neighbour(1); if (n) this.playing.set(n); }
  prev() { const n = this.neighbour(-1); if (n) this.playing.set(n); }
}
