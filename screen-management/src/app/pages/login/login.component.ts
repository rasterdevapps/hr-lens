import { Component, inject, signal } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import { MatIconModule } from '@angular/material/icon';
import { AuthService, errorMessage } from '../../core/auth.service';
import { FormErrors } from '../shared/validation';

@Component({
  selector: 'app-login',
  standalone: true,
  imports: [MatIconModule],
  templateUrl: './login.component.html',
  styleUrl: './login.component.scss',
})
export class LoginComponent {
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);

  email = '';
  password = '';
  showPassword = signal(false);
  busy = signal(false);
  error = signal('');
  /** Only the shape is checked here: the password rules apply when it is set, not when signing in */
  readonly form = new FormErrors(() => ({
    // Loose on purpose: an older account may have an address the stricter add-user check would refuse
    email: !this.email.trim() ? 'Email is required'
      : /\s/.test(this.email.trim()) || !/^[^@]+@[^@]+$/.test(this.email.trim()) ? 'Enter a valid email address, such as name@company.com' : '',
    password: this.password ? '' : 'Password is required',
  }));

  set(field: 'email' | 'password', value: string) {
    this[field] = value;
    this.error.set('');
    this.form.touch(field);
  }

  async submit() {
    if (this.busy()) return;
    this.error.set('');
    if (!this.form.submit()) return;
    this.busy.set(true);
    this.error.set('');
    try {
      await this.auth.login(this.email.trim(), this.password);
      // Only follow in-app addresses
      const target = this.route.snapshot.queryParamMap.get('returnUrl') ?? '';
      await this.router.navigateByUrl(target.startsWith('/') && !target.startsWith('//') ? target : '/dashboard');
    } catch (err) {
      this.error.set(errorMessage(err, 'Could not sign in. Please try again.'));
      this.password = '';
      this.form.reset();
    } finally {
      this.busy.set(false);
    }
  }
}
