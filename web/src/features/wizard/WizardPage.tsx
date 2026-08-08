import { useState, useEffect } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ChevronLeft, ChevronRight, Loader2, CheckCircle, AlertTriangle } from 'lucide-react';
import { Button } from '@/shared/components/ui/button';
import { Input } from '@/shared/components/ui/input';
import { Label } from '@/shared/components/ui/label';
import { Card, CardContent, CardHeader, CardTitle } from '@/shared/components/ui/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select';
import { cn } from '@/shared/utils/cn';
import { api } from '@/shared/api/client';

const TOTAL_STEPS = 6;

const MAX_LOGO_BYTES = 1024 * 1024;

// Must match the router's '/wizard/step/:step'. These previously pointed at
// '/wizard/stepN', which matched no route, so Next led to a blank page.
const stepPath = (step: number) => `/wizard/step/${step}`;

// Screen 1 is the welcome text and screen 6 is the summary; neither submits.
// The four screens in between map onto the backend's step1..step4, which is
// the off-by-one that made "Next" on the welcome screen POST an empty
// organization to /api/wizard/step1 and fail with a validation error.
const stepEndpoint: Record<number, string> = {
  2: '/wizard/step1', // Organization
  3: '/wizard/step2', // Admin user
  4: '/wizard/step3', // Authentication
  5: '/wizard/step4', // Services
};

const stepTitles = [
  'Welcome',
  'Organization',
  'Admin User',
  'Authentication',
  'Services',
  'Complete',
];

// Pairs a label with its input via a real htmlFor/id link. The wizard
// previously rendered bare <Label> elements next to inputs, which left every
// field on the auth and services screens unlabelled for screen readers.
function Field({
  id,
  label,
  ...inputProps
}: { id: string; label: string } & React.ComponentProps<typeof Input>) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} {...inputProps} />
    </div>
  );
}

interface FormData {
  orgName: string;
  orgSubdomain: string;
  orgLogo: string;
  adminName: string;
  adminEmail: string;
  adminPassword: string;
  oidcProvider: string;
  googleClientId: string;
  googleClientSecret: string;
  entraClientId: string;
  entraClientSecret: string;
  entraTenantId: string;
  customIssuer: string;
  customClientId: string;
  customClientSecret: string;
  minioEndpoint: string;
  minioAccessKey: string;
  minioSecretKey: string;
  minioBucket: string;
  livekitUrl: string;
  livekitApiKey: string;
  livekitApiSecret: string;
  smtpHost: string;
  smtpPort: number;
  smtpUser: string;
  smtpPass: string;
  smtpFrom: string;
}

const initialFormData: FormData = {
  orgName: '',
  orgSubdomain: '',
  orgLogo: '',
  adminName: '',
  adminEmail: '',
  adminPassword: '',
  oidcProvider: 'custom',
  googleClientId: '',
  googleClientSecret: '',
  entraClientId: '',
  entraClientSecret: '',
  entraTenantId: '',
  customIssuer: '',
  customClientId: '',
  customClientSecret: '',
  minioEndpoint: '',
  minioAccessKey: '',
  minioSecretKey: '',
  minioBucket: 'teamup-files',
  livekitUrl: '',
  livekitApiKey: '',
  livekitApiSecret: '',
  smtpHost: '',
  smtpPort: 587,
  smtpUser: '',
  smtpPass: '',
  smtpFrom: '',
};

export function WizardPage({ initialStep }: { initialStep?: number }) {
  const navigate = useNavigate();
  const params = useParams();
  const urlStep = params.step ? parseInt(params.step as string, 10) : (initialStep || 1);
  const [currentStep, setCurrentStep] = useState(urlStep);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  // Restored during initialisation, not in an effect: the persist effect below
  // is declared first and would otherwise overwrite the saved copy with the
  // empty defaults before a load effect ever ran, blanking the form on refresh.
  const [formData, setFormData] = useState<FormData>(() => {
    try {
      const saved = sessionStorage.getItem('wizardFormData');
      return saved ? { ...initialFormData, ...JSON.parse(saved) } : initialFormData;
    } catch {
      return initialFormData;
    }
  });
  const [testResults, setTestResults] = useState<Record<string, { status: 'idle' | 'testing' | 'success' | 'error'; message: string }>>({});

  // Sync currentStep with URL (for browser back/forward)
  useEffect(() => {
    setCurrentStep(urlStep);
  }, [urlStep]);

  // An already-provisioned instance must not present the setup form. The API
  // refuses the mutations anyway, but showing the wizard invites the user to
  // fill in a form that can only fail.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await api.get('/wizard/status');
        if (!cancelled && res.data?.complete) navigate('/', { replace: true });
      } catch {
        // Status unavailable: let the wizard render rather than dead-end.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [navigate]);

  // Persist form data to sessionStorage
  useEffect(() => {
    sessionStorage.setItem('wizardFormData', JSON.stringify(formData));
  }, [formData]);

  const handleSubmit = async (e?: React.FormEvent) => {
    e?.preventDefault();
    setLoading(true);
    setError('');
    try {
      const endpoint = stepEndpoint[currentStep];
      if (endpoint) {
        await api.post(endpoint, formData);
      }
      if (currentStep < TOTAL_STEPS) {
        navigate(stepPath(currentStep + 1));
      }
    } catch (err: any) {
      console.error('Wizard step failed:', err);
      setError(err.response?.data?.error || 'Failed to save step. Please try again.');
    } finally {
      setLoading(false);
    }
  };

  // Screen 6 finalizes: only here does the server create the organization.
  const handleComplete = async () => {
    setLoading(true);
    setError('');
    try {
      await api.post('/wizard/step5', {});
      sessionStorage.removeItem('wizardFormData');
      // Full reload so the router re-runs its setup check against the now
      // provisioned instance instead of using the cached "not set up" answer.
      window.location.assign('/');
    } catch (err: any) {
      console.error('Wizard finalization failed:', err);
      // The server keeps step state in a session that expires after 30 minutes.
      // If it lapsed, the summary screen would otherwise become a dead end with
      // a button that can only ever fail, so send the user back to the first
      // step the server is still missing (their answers are cached locally).
      const missing: number[] | undefined = err.response?.data?.missingSteps;
      if (missing?.length) {
        setError('Your setup session expired. Please re-confirm the remaining details.');
        navigate(stepPath(missing[0] + 1));
        return;
      }
      setError(err.response?.data?.error || 'Failed to complete setup. Please try again.');
    } finally {
      setLoading(false);
    }
  };

  const handleBack = () => {
    if (currentStep > 1) {
      navigate(stepPath(currentStep - 1));
    }
  };

  const testService = async (service: 'minio' | 'livekit' | 'smtp') => {
    setTestResults(prev => ({ ...prev, [service]: { status: 'testing', message: 'Testing connection...' } }));
    try {
      await api.post('/wizard/test-service', { service, ...formData });
      setTestResults(prev => ({ ...prev, [service]: { status: 'success', message: 'Connection successful!' } }));
    } catch (err: any) {
      setTestResults(prev => ({ ...prev, [service]: { status: 'error', message: err.response?.data?.error || 'Connection failed' } }));
    }
  };

  const renderStep = () => {
    switch (currentStep) {
      case 1:
        return (
          <div className="text-center space-y-8">
            <div className="mx-auto h-20 w-20 rounded-full bg-primary/10 flex items-center justify-center">
              <CheckCircle className="h-12 w-12 text-primary" />
            </div>
            <div>
              <h2 className="text-3xl font-bold">Welcome to TeamUp</h2>
              <p className="text-muted-foreground mt-2">Set up your self-hosted collaboration platform in a few steps.</p>
            </div>
            <ul className="text-left space-y-3 max-w-md mx-auto">
              {[
                'Configure your organization',
                'Create an admin account',
                'Set up authentication (OIDC)',
                'Connect external services (MinIO, LiveKit)',
                'Start collaborating!'
              ].map((item, i) => (
                <li key={i} className="flex items-center gap-3">
                  <div className="h-6 w-6 rounded-full bg-primary/10 flex items-center justify-center text-primary text-sm font-medium">
                    {i + 1}
                  </div>
                  <span>{item}</span>
                </li>
              ))}
            </ul>
          </div>
        );
      case 2:
        return (
          <form onSubmit={handleSubmit} className="space-y-6">
            <Card>
              <CardHeader>
                <CardTitle>Organization Details</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="orgName">Organization Name</Label>
                  <Input id="orgName" value={formData.orgName} onChange={e => setFormData({...formData, orgName: e.target.value})} required />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="orgSubdomain">Subdomain</Label>
                  <div className="flex items-center gap-2">
                    <Input id="orgSubdomain" value={formData.orgSubdomain} onChange={e => setFormData({...formData, orgSubdomain: e.target.value})} placeholder="myorg" required />
                    <span className="text-muted-foreground">.teamup.local</span>
                  </div>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="orgLogo">Logo (optional)</Label>
                  <Input
                    id="orgLogo"
                    type="file"
                    accept="image/*"
                    onChange={(e) => {
                      const file = e.target.files?.[0];
                      if (!file) return;
                      // Inlined as a base64 data URI into the setup session, so
                      // keep it small rather than pushing megabytes per step.
                      if (file.size > MAX_LOGO_BYTES) {
                        setError('Logo must be smaller than 1 MB.');
                        e.target.value = '';
                        return;
                      }
                      const reader = new FileReader();
                      reader.onload = () => setFormData({ ...formData, orgLogo: reader.result as string });
                      reader.readAsDataURL(file);
                    }}
                  />
                </div>
              </CardContent>
            </Card>
          </form>
        );
      case 3:
        return (
          <form onSubmit={handleSubmit} className="space-y-6">
            <Card>
              <CardHeader>
                <CardTitle>Admin Account</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="adminName">Full Name</Label>
                  <Input id="adminName" value={formData.adminName} onChange={e => setFormData({...formData, adminName: e.target.value})} required />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="adminEmail">Email</Label>
                  <Input id="adminEmail" type="email" value={formData.adminEmail} onChange={e => setFormData({...formData, adminEmail: e.target.value})} required />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="adminPassword">Password (min 12 chars)</Label>
                  <Input id="adminPassword" type="password" value={formData.adminPassword} onChange={e => setFormData({...formData, adminPassword: e.target.value})} required minLength={12} />
                </div>
              </CardContent>
            </Card>
          </form>
        );
      case 4:
        return (
          <form onSubmit={handleSubmit} className="space-y-6">
            <Card>
              <CardHeader>
                <CardTitle>Authentication Provider</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="oidcProvider">Provider</Label>
                  <Select value={formData.oidcProvider} onValueChange={v => setFormData({...formData, oidcProvider: v})}>
                    <SelectTrigger id="oidcProvider" aria-label="Provider"><SelectValue placeholder="Select provider" /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="google">Google Workspace</SelectItem>
                      <SelectItem value="entra">Microsoft Entra ID</SelectItem>
                      <SelectItem value="custom">Custom OIDC</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                {formData.oidcProvider === 'google' && (
                  <div className="grid gap-4 md:grid-cols-2">
                    <Field id="googleClientId" label="Client ID" value={formData.googleClientId} onChange={e => setFormData({...formData, googleClientId: e.target.value})} />
                    <Field id="googleClientSecret" label="Client Secret" type="password" value={formData.googleClientSecret} onChange={e => setFormData({...formData, googleClientSecret: e.target.value})} />
                  </div>
                )}
                {formData.oidcProvider === 'entra' && (
                  <div className="grid gap-4 md:grid-cols-3">
                    <Field id="entraClientId" label="Client ID" value={formData.entraClientId} onChange={e => setFormData({...formData, entraClientId: e.target.value})} />
                    <Field id="entraClientSecret" label="Client Secret" type="password" value={formData.entraClientSecret} onChange={e => setFormData({...formData, entraClientSecret: e.target.value})} />
                    <Field id="entraTenantId" label="Tenant ID" value={formData.entraTenantId} onChange={e => setFormData({...formData, entraTenantId: e.target.value})} />
                  </div>
                )}
                {formData.oidcProvider === 'custom' && (
                  <div className="grid gap-4 md:grid-cols-3">
                    <Field id="customIssuer" label="Issuer URL" value={formData.customIssuer} onChange={e => setFormData({...formData, customIssuer: e.target.value})} />
                    <Field id="customClientId" label="Client ID" value={formData.customClientId} onChange={e => setFormData({...formData, customClientId: e.target.value})} />
                    <Field id="customClientSecret" label="Client Secret" type="password" value={formData.customClientSecret} onChange={e => setFormData({...formData, customClientSecret: e.target.value})} />
                  </div>
                )}
              </CardContent>
            </Card>
          </form>
        );
      case 5:
        return (
          <form onSubmit={handleSubmit} className="space-y-6">
            <Card>
              <CardHeader>
                <CardTitle>MinIO (File Storage)</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid gap-4 md:grid-cols-2">
                  <Field id="minioEndpoint" label="Endpoint" value={formData.minioEndpoint} onChange={e => setFormData({...formData, minioEndpoint: e.target.value})} placeholder="minio:9000" />
                  <Field id="minioBucket" label="Bucket" value={formData.minioBucket} onChange={e => setFormData({...formData, minioBucket: e.target.value})} />
                  <Field id="minioAccessKey" label="Access Key" value={formData.minioAccessKey} onChange={e => setFormData({...formData, minioAccessKey: e.target.value})} />
                  <Field id="minioSecretKey" label="Secret Key" type="password" value={formData.minioSecretKey} onChange={e => setFormData({...formData, minioSecretKey: e.target.value})} />
                </div>
                <Button type="button" variant="outline" onClick={() => testService('minio')} disabled={loading}>
                  {testResults.minio?.status === 'testing' ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : 'Test MinIO Connection'}
                </Button>
                {testResults.minio && (
                  <div className={cn('text-sm', testResults.minio.status === 'success' ? 'text-green-600' : 'text-red-600')}>
                    {testResults.minio.message}
                  </div>
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>LiveKit (Video Calls)</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid gap-4 md:grid-cols-3">
                  <Field id="livekitUrl" label="Server URL" value={formData.livekitUrl} onChange={e => setFormData({...formData, livekitUrl: e.target.value})} placeholder="http://livekit:7880" />
                  <Field id="livekitApiKey" label="API Key" value={formData.livekitApiKey} onChange={e => setFormData({...formData, livekitApiKey: e.target.value})} />
                  <Field id="livekitApiSecret" label="API Secret" type="password" value={formData.livekitApiSecret} onChange={e => setFormData({...formData, livekitApiSecret: e.target.value})} />
                </div>
                <Button type="button" variant="outline" onClick={() => testService('livekit')} disabled={loading}>
                  {testResults.livekit?.status === 'testing' ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : 'Test LiveKit Connection'}
                </Button>
                {testResults.livekit && (
                  <div className={cn('text-sm', testResults.livekit.status === 'success' ? 'text-green-600' : 'text-red-600')}>
                    {testResults.livekit.message}
                  </div>
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>SMTP (Email - Optional)</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid gap-4 md:grid-cols-4">
                  <Field id="smtpHost" label="Host" value={formData.smtpHost} onChange={e => setFormData({...formData, smtpHost: e.target.value})} />
                  <Field id="smtpPort" label="Port" type="number" value={formData.smtpPort} onChange={e => setFormData({...formData, smtpPort: parseInt(e.target.value, 10) || 0})} />
                  <Field id="smtpUser" label="User" value={formData.smtpUser} onChange={e => setFormData({...formData, smtpUser: e.target.value})} />
                  <Field id="smtpPass" label="Password" type="password" value={formData.smtpPass} onChange={e => setFormData({...formData, smtpPass: e.target.value})} />
                </div>
                <Field id="smtpFrom" label="From Email" value={formData.smtpFrom} onChange={e => setFormData({...formData, smtpFrom: e.target.value})} />
                <Button type="button" variant="outline" onClick={() => testService('smtp')} disabled={loading}>
                  {testResults.smtp?.status === 'testing' ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : 'Test SMTP Connection'}
                </Button>
                {testResults.smtp && (
                  <div className={cn('text-sm', testResults.smtp.status === 'success' ? 'text-green-600' : 'text-red-600')}>
                    {testResults.smtp.message}
                  </div>
                )}
              </CardContent>
            </Card>
          </form>
        );
      case 6:
        return (
          <div className="space-y-8">
            <div className="text-center space-y-2">
              <div className="mx-auto h-20 w-20 rounded-full bg-primary/10 flex items-center justify-center">
                <CheckCircle className="h-12 w-12 text-primary" />
              </div>
              <h2 className="text-3xl font-bold">Review &amp; Finish</h2>
              <p className="text-muted-foreground">
                Confirm the details below, then create your organization.
              </p>
            </div>
            <Card>
              <CardHeader>
                <CardTitle>Summary</CardTitle>
              </CardHeader>
              <CardContent>
                <dl className="grid gap-3 sm:grid-cols-2 text-sm">
                  {[
                    ['Organization', formData.orgName],
                    ['Subdomain', formData.orgSubdomain],
                    ['Admin', formData.adminName],
                    ['Admin email', formData.adminEmail],
                    ['Auth provider', formData.oidcProvider],
                    ['MinIO endpoint', formData.minioEndpoint],
                    ['MinIO bucket', formData.minioBucket],
                    ['LiveKit URL', formData.livekitUrl],
                    ['SMTP host', formData.smtpHost || 'Not configured'],
                  ].map(([label, value]) => (
                    <div key={label as string}>
                      <dt className="text-muted-foreground">{label}</dt>
                      <dd className="font-medium break-all">{value || '—'}</dd>
                    </div>
                  ))}
                </dl>
              </CardContent>
            </Card>
          </div>
        );
      default:
        return null;
    }
  };

  return (
    <div className="flex min-h-screen bg-background">
      <div className="flex-1 max-w-3xl mx-auto py-12 px-4">
        {/* Progress indicator */}
        <div className="mb-8">
          <div className="flex items-center justify-between mb-4">
            {stepTitles.map((title, i) => (
              <React.Fragment key={i}>
                <div className={cn(
                  'flex items-center justify-center',
                  i < currentStep - 1 ? 'text-primary' : i === currentStep - 1 ? 'text-primary' : 'text-muted-foreground'
                )}>
                  {i < currentStep - 1 ? (
                    <CheckCircle className="h-6 w-6" />
                  ) : (
                    <span className="h-6 w-6 rounded-full border-2 border-current flex items-center justify-center text-sm font-medium">
                      {i + 1}
                    </span>
                  )}
                </div>
                {i < stepTitles.length - 1 && (
                  <div className={cn(
                    'flex-1 h-1 mx-2',
                    i < currentStep - 1 ? 'bg-primary' : 'bg-border'
                  )} />
                )}
              </React.Fragment>
            ))}
          </div>
          <div className="flex justify-between text-sm text-muted-foreground">
            {stepTitles.map((title, i) => (
              <span key={i} className={cn(
                'w-20 text-center',
                i === currentStep - 1 ? 'font-medium text-foreground' : ''
              )}>
                {title}
              </span>
            ))}
          </div>
        </div>

        {/* Step content */}
        <Card className="w-full">
          <CardContent className="p-6 md:p-8">
            {error && (
              <div className="mb-6 flex items-center gap-3 p-4 rounded-lg bg-destructive/10 border border-destructive/20 text-destructive">
                <AlertTriangle className="h-5 w-5 flex-shrink-0" />
                <span className="text-sm">{error}</span>
              </div>
            )}
            {renderStep()}
          </CardContent>
        </Card>

        {/* Navigation */}
        <div className="mt-6 flex justify-between">
          <Button variant="outline" onClick={handleBack} disabled={currentStep === 1}>
            <ChevronLeft className="mr-2 h-4 w-4" />
            Back
          </Button>
          <Button onClick={currentStep === TOTAL_STEPS ? handleComplete : handleSubmit} disabled={loading}>
            {currentStep === TOTAL_STEPS ? 'Create Organization' : 'Next'}
            {currentStep !== TOTAL_STEPS && <ChevronRight className="ml-2 h-4 w-4" />}
            {loading && <Loader2 className="ml-2 h-4 w-4 animate-spin" />}
          </Button>
        </div>
      </div>
    </div>
  );
}

import React from 'react';
