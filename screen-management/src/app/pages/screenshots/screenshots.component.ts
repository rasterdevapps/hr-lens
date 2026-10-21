import { pipeOffset } from '../shared/period';
import { Component, computed, effect, HostListener, inject, signal } from '@angular/core';
import { DatePipe, formatDate } from '@angular/common';
import { provideNativeDateAdapter } from '@angular/material/core';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { ActivityLevelsComponent } from './activity-levels.component';
import { ScreenRecordingsComponent } from './screen-recordings.component';
import { PeoplePanelComponent, Selection } from '../people-panel/people-panel.component';
import { addDays, Employee, startOfDay, TimeTrackerService } from '../dashboard/time-tracker.service';
import { AuthService } from '../../core/auth.service';
import { Capture, MonitoringService } from '../../core/monitoring.service';
import { loadOnChange } from '../shared/load';
import { isoDate } from '../shared/period';
import { LiveScreenComponent } from './live-screen.component';

interface Shot { id: number; url: string; app: string; title: string; time: string; }
interface Group { label: string; shots: Shot[]; }

// tz: the zone the server counts days in. Format times with pipeOffset(tz, at), which follows daylight saving
export const ZONES = [
  { code: 'IST', label: 'India (UTC+5:30)', pipe: '+0530', tz: 'Asia/Kolkata' },
  { code: 'GST', label: 'Gulf (UTC+4:00)', pipe: '+0400', tz: 'Asia/Dubai' },
  { code: 'UTC', label: 'UTC', pipe: '+0000', tz: 'UTC' },
  { code: 'EST', label: 'US Eastern', pipe: '-0500', tz: 'America/New_York' },
  { code: 'PST', label: 'US Pacific', pipe: '-0800', tz: 'America/Los_Angeles' },
];
export type Zone = (typeof ZONES)[number];

@Component({
  selector: 'app-screenshots',
  standalone: true,
  imports: [DatePipe, MatDatepickerModule, MatIconModule, MatMenuModule, MatTooltipModule, RouterLink,
    PeoplePanelComponent, ScreenRecordingsComponent, ActivityLevelsComponent, LiveScreenComponent],
  providers: [provideNativeDateAdapter()],
  templateUrl: './screenshots.component.html',
  // Shares the toolbar, tab and page-frame styles with the Time Tracker pages
  styleUrls: ['../dashboard/dashboard.component.scss', './screenshots.component.scss'],
})
export class ScreenshotsComponent {
  private readonly data = inject(TimeTrackerService);
  private readonly api = inject(MonitoringService);

  readonly zones = ZONES;
  private readonly auth = inject(AuthService);

  /** The tabs this role may open: a user gets Screenshots only */
  readonly tabs = [
    { key: 'screenshots', label: 'Screenshots', icon: 'photo_camera', route: '/screenshots' },
    { key: 'screen-recordings', label: 'Screen Recordings', icon: 'videocam', route: '/screen-recordings' },
    { key: 'activity-levels', label: 'Activity Levels', icon: 'bar_chart', route: '/activity-levels' },
    { key: 'live', label: 'Live Screen', icon: 'live_tv', route: '/live-screen-monitoring' },
  ].filter(t => this.auth.can(t.route));
  readonly tab: string = inject(ActivatedRoute).snapshot.data['tab'] ?? 'screenshots';
  readonly sizes = [
    { label: 'Tiny', width: 96 },
    { label: 'Small', width: 136 },
    { label: 'Medium', width: 182 },
    { label: 'Large', width: 264 },
  ];
  readonly today = startOfDay(new Date());

  selection = signal<Selection>(this.data.selection());
  // The day and zone carry over between the Monitoring tabs too
  date = signal(this.data.monitoringDay?.date ?? this.today);
  zone = signal(ZONES.find(z => z.code === this.data.monitoringDay?.zone) ?? ZONES[0]);
  private readonly keepSelection = effect(() => {
    this.data.rememberSelection(this.selection());
    this.data.monitoringDay = { date: this.date(), zone: this.zone().code };
  });
  size = signal(this.sizes[2]);
  newestFirst = signal(true);
  preview = signal<Shot | null>(null);

  // Enlarged view zoom
  readonly minZoom = 1;
  readonly maxZoom = 5;
  zoom = signal(1);
  dragging = false;
  private dragFrom = { x: 0, y: 0, left: 0, top: 0 };
  private picked = signal<number | null>(null);

  members = computed<Employee[]>(() => {
    const s = this.selection();
    return s.kind === 'employee' ? [s.employee] : s.team.members;
  });

  /** Screenshots are per person: with a team selected, one member is shown at a time */
  employee = computed(() => this.members().find(e => e.id === this.picked()) ?? this.members()[0]);

  title = computed(() => {
    const s = this.selection();
    return s.kind === 'employee' ? s.employee.name : `${s.team.name} (${s.team.members.length})`;
  });

  atEnd = computed(() => this.date() >= this.today);

  // ---------- Screenshots from the agent ----------
  private attempt = signal(0);
  private readonly request = computed(() => ({
    users: [this.employee().id], from: isoDate(this.date()), to: isoDate(this.date()), tz: this.zone().tz, attempt: this.attempt(),
  }));
  readonly shots = loadOnChange(this.request, r => this.api.captures('screenshot', r));

  retry() { this.attempt.update(n => n + 1); }

  /** Grouped by hour in the selected zone */
  groups = computed<Group[]>(() => {
    const list: Capture[] = this.shots().data ?? [];
    const tz = this.zone().tz;
    const groups: Group[] = [];
    for (const c of list) {
      const pipe = pipeOffset(tz, new Date(c.startedAt));
      const hour = Number(formatDate(c.startedAt, 'H', 'en-US', pipe));
      const label = `${String(hour).padStart(2, '0')}:00 - ${String((hour + 1) % 24).padStart(2, '0')}:00`;
      let group = groups[groups.length - 1];
      if (group?.label !== label) groups.push(group = { label, shots: [] });
      group.shots.push({
        id: c.id, url: this.api.fileUrl(c.id), app: c.app || 'Unknown', title: c.title,
        time: formatDate(c.startedAt, 'HH:mm', 'en-US', pipe),
      });
    }
    return this.newestFirst() ? groups.reverse().map(g => ({ ...g, shots: g.shots.reverse() })) : groups;
  });

  count = computed(() => this.groups().reduce((a, g) => a + g.shots.length, 0));

  pick(e: Employee) { this.picked.set(e.id); }

  shift(step: number) {
    const next = addDays(this.date(), step);
    if (next <= this.today) this.date.set(next);
  }

  // ---------- Enlarged view ----------
  open(shot: Shot) {
    this.zoom.set(1);
    this.preview.set(shot);
  }

  close() { this.preview.set(null); }

  zoomBy(step: number) {
    this.zoom.update(z => Math.min(this.maxZoom, Math.max(this.minZoom, z + step)));
  }

  resetZoom() { this.zoom.set(1); }

  onWheel(event: WheelEvent) {
    event.preventDefault();
    this.zoomBy(event.deltaY < 0 ? 0.5 : -0.5);
  }

  startDrag(event: PointerEvent, el: HTMLElement) {
    if (this.zoom() === 1) return;
    this.dragging = true;
    this.dragFrom = { x: event.clientX, y: event.clientY, left: el.scrollLeft, top: el.scrollTop };
    el.setPointerCapture(event.pointerId);
  }

  drag(event: PointerEvent, el: HTMLElement) {
    if (!this.dragging) return;
    el.scrollLeft = this.dragFrom.left - (event.clientX - this.dragFrom.x);
    el.scrollTop = this.dragFrom.top - (event.clientY - this.dragFrom.y);
  }

  @HostListener('document:keydown', ['$event'])
  onKey(event: KeyboardEvent) {
    if (!this.preview()) return;
    if (event.key === 'Escape') this.close();
    else if (event.key === '+' || event.key === '=') this.zoomBy(0.5);
    else if (event.key === '-') this.zoomBy(-0.5);
    else if (event.key === '0') this.resetZoom();
  }
}
