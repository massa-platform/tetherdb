# Constraint: Private Network Database Access — 2026-06-17

**Type:** technical
**Enforced by:** customer network topology / firewall rules
**Verified:** 2026-06-17

---

## The Constraint

SQL Server (and potentially PostgreSQL) instances may run on private networks with no
inbound internet access. The SQL Server port (1433) is commonly blocked at the firewall
and is not exposed to the public internet. tetherdb cannot rely on being able to reach
the databases from an external service or cloud host.

## What This Rules Out

- A hosted SaaS sync service that dials into customer databases from the cloud
- Any architecture that requires inbound firewall rules or port forwarding to SQL Server
- Assuming the tool can be run on a machine that is not co-located with or VPN-connected to the database network

## What Must Be Done

- tetherdb must be deployed **inside** the customer's private network — on the same host, same LAN, or behind the same VPN as the databases
- The tool connects **outward** to both databases; it does not listen for inbound connections from databases
- If tetherdb has any external component (e.g., a management UI or status API), it must be optional and the core sync must work fully air-gapped
- Connection configuration (hosts, ports, credentials) must support private hostnames and non-standard ports

## References

User statement (2026-06-17): "the dbs might be running privately not accessible to the internet especially ms sql server port is not open"
