import { Component, computed, DestroyRef, effect, ElementRef, inject, input, signal, untracked } from '@angular/core';
import { DatePipe } from '@angular/common';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { firstValueFrom } from 'rxjs';
import { LiveStatus, MonitoringService } from '../../core/monitoring.service';
import { Employee } from '../dashboard/time-tracker.service';

const STATUS_EVERY = 5000;   // who is online; also keeps their agents sending frames
const FRAME_EVERY = 2000;    // new picture per person
const MAX_TILES = 12;

const AVATARS = ['#4f46e5', '#0ea5e9', '#f97316', '#14b8a6', '#a855f7', '#ec4899', '#22c55e', '#64748b'];

/** Monitoring > Live Screen: a picture of each person's screen, refreshed every 2 seconds */
@Component({
  selector: 'app-live-screen',
  standalone: true,
  imports: [DatePipe, MatIconModule, MatTooltipModule],
  templateUrl: './live-screen.component.html',
  styleUrl: './live-screen.component.scss',
})
export class LiveScreenComponent {
  private readonly api = inject(MonitoringService);

  members = input.required<Employee[]>();

  readonly shown = computed(() => this.members().slice(0, MAX_TILES));
  readonly hidden = computed(() => Math.max(this.members().length - MAX_TILES, 0));

  private readonly status = signal<ReadonlyMap<number, LiveStatus>>(new Map());
  /** Object URL of the newest frame per person */
  readonly frames = signal<ReadonlyMap<number, { url: string; at: Date }>>(new Map());
  readonly loaded = signal(false);
  readonly error = signal('');
  readonly enlarged = signal<Employee | null>(null);

  /** The whole Live Screen view, which can go full screen with every tile in it */
  readonly host: HTMLElement = inject(ElementRef).nativeElement;
  readonly canFullscreen = document.fullscreenEnabled;
  /** The element currently shown full screen, if it is one of ours */
  readonly fullscreen = signal<Element | null>(null);

  constructor() {
    const onFullscreen = () => this.fullscreen.set(document.fullscreenElement);
    document.addEventListener('fullscreenchange', onFullscreen);
    const statusTimer = setInterval(() => this.refreshStatus(), STATUS_EVERY);
    const frameTimer = setInterval(() => this.refreshFrames(), FRAME_EVERY);
    // A new person or team: ask about them straight away
    effect(() => {
      this.shown();
      untracked(() => this.refreshStatus());
    });
    inject(DestroyRef).onDestroy(() => {
      document.removeEventListener('fullscreenchange', onFullscreen);
      if (this.host.contains(document.fullscreenElement)) document.exitFullscreen().catch(() => {});
      clearInterval(statusTimer);
      clearInterval(frameTimer);
      this.frames().forEach(f => URL.revokeObjectURL(f.url));
    });
  }

  /** Shows the element on the whole monitor, or back in the page when it already is */
  toggleFullscreen(el: Element) {
    const done = document.fullscreenElement === el ? document.exitFullscreen() : el.requestFullscreen();
    done.catch(() => {}); // the browser refused (e.g. not started by a click): stay as we are
  }

  closeEnlarged() {
    if (document.fullscreenElement?.classList.contains('hl-cinema')) document.exitFullscreen().catch(() => {});
    this.enlarged.set(null);
  }

  tile(e: Employee) {
    const st = this.status().get(e.id);
    const frame = this.frames().get(e.id);
    const state = !st?.online ? 'offline' : frame ? 'live' : st.noScreen ? 'noscreen' : 'connecting';
    return {
      state, frame, device: st?.device ?? '', lastSeen: st?.lastSeen ?? null,
      initials: e.name.split(' ').map(p => p[0]).join('').slice(0, 2).toUpperCase(),
      color: AVATARS[e.id % AVATARS.length],
    };
  }

  private async refreshStatus() {
    const ids = this.shown().map(m => m.id);
    if (!ids.length) return;
    try {
      const list = await firstValueFrom(this.api.live(ids));
      this.status.set(new Map(list.map(s => [s.userId, s])));
      this.error.set('');
    } catch {
      this.error.set('Cannot reach the server. Live view will resume when it is back.');
    } finally {
      this.loaded.set(true);
    }
  }

  private async refreshFrames() {
    const online = this.shown().filter(m => this.status().get(m.id)?.online && !this.status().get(m.id)?.noScreen);
    await Promise.all(online.map(async m => {
      try {
        const res = await firstValueFrom(this.api.frame(m.id));
        if (res.status !== 200 || !res.body) return;
        const url = URL.createObjectURL(res.body);
        const at = new Date(res.headers.get('X-Frame-Time') ?? Date.now());
        this.frames.update(map => {
          const next = new Map(map);
          const old = next.get(m.id);
          if (old) URL.revokeObjectURL(old.url);
          next.set(m.id, { url, at });
          return next;
        });
      } catch {
        // Keep the last picture; the status check reports the person offline if the agent stopped
      }
    }));
    // Drop pictures of people who went offline
    this.frames.update(map => {
      let changed = false;
      const next = new Map(map);
      for (const [id, f] of next) {
        if (!this.status().get(id)?.online) {
          URL.revokeObjectURL(f.url);
          next.delete(id);
          changed = true;
        }
      }
      return changed ? next : map;
    });
  }
}
