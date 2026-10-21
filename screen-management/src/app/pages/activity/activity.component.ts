import { Component, computed, effect, inject, signal } from '@angular/core';
import { DatePipe, DecimalPipe } from '@angular/common';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Observable } from 'rxjs';
import {
  ActivityEvents, ActivityGroup, ActivityQuery, ActivityService, ActivitySummary, Productivity,
} from '../../core/activity.service';
import { PeoplePanelComponent, Selection } from '../people-panel/people-panel.component';
import { Employee, hash, TimeTrackerService } from '../dashboard/time-tracker.service';
import { Loaded, loadOnChange } from '../shared/load';
import { PeriodFilter } from '../shared/period';
import { PeriodToolbarComponent } from '../shared/period-toolbar.component';

type Tab = 'websites-visited' | 'applications-used' | 'by-categories' | 'detailed-activity';

interface Request { query: ActivityQuery; page: number; dir: 'asc' | 'desc'; attempt: number; }

const PAGE_SIZE = 50;

const GROUPS: Record<Exclude<Tab, 'detailed-activity'>, ActivityGroup> = {
  'websites-visited': 'website',
  'applications-used': 'application',
  'by-categories': 'category',
};

const TILE_COLORS = ['#4f46e5', '#0ea5e9', '#f97316', '#14b8a6', '#a855f7', '#ec4899', '#22c55e', '#64748b'];

/** Websites Visited, Applications Used, By Categories and Detailed Activity, from GET /api/activity/* */
@Component({
  selector: 'app-activity',
  standalone: true,
  imports: [
    DatePipe, DecimalPipe, RouterLink, MatIconModule, MatTooltipModule, PeoplePanelComponent, PeriodToolbarComponent,
  ],
  templateUrl: './activity.component.html',
  // Toolbar, tabs, cards and tables come from the Time Tracker page
  styleUrls: ['../dashboard/dashboard.component.scss', './activity.component.scss'],
})
export class ActivityComponent {
  private readonly people = inject(TimeTrackerService);
  private readonly api = inject(ActivityService);

  readonly pageSize = PAGE_SIZE;

  readonly tabs: { key: Tab; label: string; icon: string; route: string; column: string; plural: string }[] = [
    { key: 'websites-visited', label: 'Websites Visited', icon: 'language', route: '/websites-visited', column: 'Website', plural: 'websites' },
    { key: 'applications-used', label: 'Applications Used', icon: 'apps', route: '/applications-used', column: 'Application', plural: 'applications' },
    { key: 'by-categories', label: 'By Categories', icon: 'category', route: '/by-categories', column: 'Category', plural: 'categories' },
    { key: 'detailed-activity', label: 'Detailed Activity', icon: 'history', route: '/detailed-activity', column: '', plural: 'apps, titles, websites' },
  ];
  readonly tab: Tab = inject(ActivatedRoute).snapshot.data['tab'] ?? 'websites-visited';
  readonly current = this.tabs.find(t => t.key === this.tab)!;
  readonly detailed = this.tab === 'detailed-activity';

  readonly levels: { value: Productivity | ''; label: string }[] = [
    { value: '', label: 'All' },
    { value: 'productive', label: 'Productive' },
    { value: 'neutral', label: 'Neutral' },
    { value: 'unproductive', label: 'Unproductive' },
  ];

  // ---------- Filters ----------
  selection = signal<Selection>(this.people.selection());
  // Keep the pick when moving to another tab or page
  private readonly keepSelection = effect(() => this.people.rememberSelection(this.selection()));

  readonly period = new PeriodFilter();
  productivity = signal<Productivity | ''>('');
  search = signal('');
  /** The page, remembered together with the filters it was chosen for */
  private readonly pageFor = signal<{ key: string; page: number }>({ key: '', page: 0 });
  newestFirst = signal(true);
  private attempt = signal(0);   // bumped by Try again

  members = computed<Employee[]>(() => {
    const s = this.selection();
    return s.kind === 'employee' ? [s.employee] : s.team.members;
  });

  title = computed(() => {
    const s = this.selection();
    return s.kind === 'employee' ? s.employee.name : `${s.team.name} (${s.team.members.length})`;
  });

  // ---------- Data ----------
  private readonly query = computed<ActivityQuery>(() => ({
    ...this.period.apiRange(),
    users: this.members().map(m => m.id),
    productivity: this.productivity(),
    search: this.search().trim(),
  }));

  private readonly queryKey = computed(() => JSON.stringify([this.query(), this.newestFirst()]));
  /** Back to the first page whenever the filters, period, people or order change */
  readonly page = computed(() => (this.pageFor().key === this.queryKey() ? this.pageFor().page : 0));

  private readonly request = computed<Request>(() => ({
    query: this.query(),
    page: this.detailed ? this.page() : 0,
    dir: this.newestFirst() ? 'desc' : 'asc',
    attempt: this.attempt(),
  }));

  readonly summary = this.load<ActivitySummary>(!this.detailed,
    r => this.api.summary(GROUPS[this.tab as keyof typeof GROUPS], r.query));
  readonly events = this.load<ActivityEvents>(this.detailed, r => this.api.events(r.query, r.page, PAGE_SIZE, r.dir));

  /** Loads only on the tab that shows it */
  private load<T>(enabled: boolean, fetch: (r: Request) => Observable<T>) {
    if (!enabled) return signal<Loaded<T>>({ loading: false, data: null, error: '' }).asReadonly();
    return loadOnChange(this.request, fetch);
  }

  setPage(n: number) { this.pageFor.set({ key: this.queryKey(), page: Math.max(0, n) }); }
  setProductivity(value: Productivity | '') { this.productivity.set(value); }
  setSearch(value: string) { this.search.set(value); }
  toggleOrder() { this.newestFirst.update(v => !v); }
  retry() { this.attempt.update(n => n + 1); }

  /** Productive / neutral / unproductive shares of the total, for the summary bar */
  split = computed(() => {
    const t = this.summary().data?.totals;
    if (!t?.seconds) return [];
    return (['productive', 'neutral', 'unproductive'] as const).map(key => ({
      key, label: key[0].toUpperCase() + key.slice(1), seconds: t[key], pct: (t[key] / t.seconds) * 100,
    }));
  });

  topItem = computed(() => this.summary().data?.items[0] ?? null);

  pageRange = computed(() => {
    const total = this.events().data?.total ?? 0;
    const start = this.page() * PAGE_SIZE;
    return { first: total ? start + 1 : 0, last: Math.min(start + PAGE_SIZE, total), total };
  });

  // ---------- Display helpers ----------
  duration(seconds: number) {
    const h = Math.floor(seconds / 3600), m = Math.floor((seconds % 3600) / 60), s = seconds % 60;
    if (h) return `${h}h ${String(m).padStart(2, '0')}m`;
    if (m) return `${m}m ${String(s).padStart(2, '0')}s`;
    return `${s}s`;
  }

  tile(name: string) {
    return { letter: (name.replace(/^www\./, '')[0] ?? '?').toUpperCase(), color: TILE_COLORS[Math.abs(hash(name)) % TILE_COLORS.length] };
  }

  icon(p: Productivity) {
    return p === 'productive' ? 'check_circle' : p === 'unproductive' ? 'cancel' : 'remove_circle';
  }
}
