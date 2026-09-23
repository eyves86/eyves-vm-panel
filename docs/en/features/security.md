# Security Alerts

EyvesCloud includes lightweight security alerts based on connection behavior. It does not keep full normal connection logs; it focuses on abnormal behavior and high-risk patterns.

## Covered Scenarios

- Port scanning.
- Lateral scanning.
- Brute-force tendencies.
- SMTP abuse.
- UDP reflection risk.
- Suspicious ports related to mining, proxies, VPNs, Tor, and similar services.

## APIs

```http
GET /api/v1/security/alerts
POST /api/v1/security/check
GET /api/v1/security/logs?container={name}
GET /api/v1/security/summary
GET /api/v1/security/settings
PUT /api/v1/security/settings
```

## Automatic Shutdown

Security settings can enable automatic shutdown after alerts. Before enabling it, observe for a while and make sure the rules do not affect normal services.

## Logging Advice

Security alerts are risk signals. They should not replace a professional firewall, intrusion detection, or centralized logging system. For public services, still combine them with security groups, firewall rules, Fail2ban, and similar tools.
