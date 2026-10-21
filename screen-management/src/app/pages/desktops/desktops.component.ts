import { Component, computed, DestroyRef, HostListener, inject, signal } from '@angular/core';
import { DatePipe } from '@angular/common';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { firstValueFrom } from 'rxjs';
import { errorMessage } from '../../core/auth.service';
import { ago, avatarColor, Desktop, DesktopList, initials, newerVersion, WorkplaceService } from '../../core/workplace.service';

type Showing = 'active' | 'removed';
type StateFilter = 'all' | 'online' | 'offline' | 'outdated' | 'silent';

const REFRESH_MS = 30_000;
const SILENT_AFTER_DAYS = 7;

/** Desktops: every computer running the HR Lens agent */
@Component({
  selector: 'app-desktops',
  standalone: true,
  imports: [DatePipe, MatIconModule, MatTooltipModule],
  templateUrl: './desktops.component.html',
  styleUrl: './desktops.component.scss',
})
export class DesktopsComponent {
  private readonly api = inject(WorkplaceService);

  showing = signal<Showing>('active');
  filter = signal<StateFilter>('all');
  search = signal('');

  list = signal<DesktopList>({ latest: {}, items: [] });
  loading = signal(true);
  error = signal('');
  notice = signal('');
  now = signal(Date.now());

  readonly filters: { key: StateFilter; label: string }[] = [
    { key: 'all', label: 'All' },
    { key: 'online', label: 'Online' },
    { key: 'offline', label: 'Offline' },
    { key: 'outdated', label: 'Needs update' },
    { key: 'silent', label: `Silent ${SILENT_AFTER_DAYS}+ days` },
  ];

  /** Every row with what the page shows about it */
  readonly rows = computed(() => {
    const { latest, items } = this.list();
    const now = this.now();
    return items.map(d => {
      const newest = latest[d.platform] ?? '';
      const seen = d.lastSeenAt ? new Date(d.lastSeenAt).getTime() : 0;
      return {
        d,
        newest,
        outdated: !!newest && !!d.agentVersion && newerVersion(newest, d.agentVersion),
        silent: !d.online && now - seen > SILENT_AFTER_DAYS * 86_400_000,
        seen: ago(d.lastSeenAt, now),
        osLabel: osLabel(d.os),
        osIcon: osIcon(d.os),
        initials: initials(d.user.name),
        color: avatarColor(d.user.id),
      };
    });
  });

  readonly stats = computed(() => {
    const r = this.rows();
    return {
      total: r.length,
      online: r.filter(x => x.d.online).length,
      outdated: r.filter(x => x.outdated).length,
      silent: r.filter(x => x.silent).length,
      people: new Set(r.map(x => x.d.user.id)).size,
    };
  });

  readonly visible = computed(() => {
    const q = this.search().trim().toLowerCase();
    const f = this.filter();
    return this.rows().filter(x =>
      (f === 'all' || (f === 'online' && x.d.online) || (f === 'offline' && !x.d.online) ||
       (f === 'outdated' && x.outdated) || (f === 'silent' && x.silent)) &&
      (!q || [x.d.name, x.d.user.name, x.d.user.email, x.d.user.team, x.d.os, x.d.agentVersion].some(v => v.toLowerCase().includes(q))));
  });

  readonly latestLabel = computed(() =>
    Object.entries(this.list().latest).map(([p, v]) => `${osLabel(p.replace('-', '/'))} ${v}`).join(' · '));

  // ---------- Remove ----------
  removing = signal<Desktop | null>(null);
  busy = signal(false);
  removeError = signal('');

  constructor() {
    this.load();
    const timer = setInterval(() => this.load(true), REFRESH_MS);
    inject(DestroyRef).onDestroy(() => clearInterval(timer));
  }

  private loadSeq = 0;

  async load(quiet = false) {
    const seq = ++this.loadSeq, showing = this.showing();
    if (!quiet) this.loading.set(true);
    try {
      const list = await firstValueFrom(this.api.desktops(showing, quiet));
      if (seq !== this.loadSeq) return;   // a newer call (e.g. another tab) has taken over
      this.list.set(list);
      this.error.set('');
    } catch (err) {
      if (seq !== this.loadSeq) return;
      if (!quiet) this.list.set({ latest: {}, items: [] });
      this.error.set(errorMessage(err, 'Could not load computers.'));
    } finally {
      this.now.set(Date.now());
      this.loading.set(false);
    }
  }

  show(s: Showing) {
    if (this.showing() === s) return;
    this.showing.set(s);
    this.filter.set('all');
    this.notice.set('');
    this.load();
  }

  askRemove(d: Desktop) {
    this.removeError.set('');
    this.removing.set(d);
  }

  async remove() {
    const d = this.removing();
    if (!d || this.busy()) return;
    this.busy.set(true);
    try {
      await firstValueFrom(this.api.removeDesktop(d.id));
      this.removing.set(null);
      this.notice.set(`${d.name} removed. Its agent stops sending until it is registered again.`);
      await this.load();
    } catch (err) {
      this.removeError.set(errorMessage(err));
    } finally {
      this.busy.set(false);
    }
  }

  @HostListener('document:keydown.escape')
  onEscape() { this.removing.set(null); }
}

function osLabel(os: string) {
  const [name, arch] = os.split('/');
  const pretty: Record<string, string> = { linux: 'Linux', windows: 'Windows', darwin: 'macOS' };
  return [pretty[name] ?? (name || 'Unknown'), arch].filter(Boolean).join(' · ');
}

function osIcon(os: string) {
  if (os.startsWith('darwin')) return 'laptop_mac';
  if (os.startsWith('windows')) return 'laptop_windows';
  return 'computer';
}
