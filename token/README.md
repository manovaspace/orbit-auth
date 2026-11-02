# Orbit access token package

`github.com/manovaspace/orbit-auth/token` owns the shared RS256 access-token
format used by Orbit Auth and trusted consumers.

## Contract

- Orbit Auth loads an RSA PKCS#8 or PKCS#1 private PEM with
  `LoadRSAPrivateKey` and calls `SignAccessToken` with its active key ID.
- The gateway loads public PEMs named `<kid>.pem` with `LoadKeySet`, verifies
  tokens with `ParseAccessToken`, and publishes `KeySet.Document()` as JWKS.
- Product APIs use `NewRemoteVerifier` to fetch/cache JWKS and refresh on an
  unknown `kid`.
- Tokens use RS256, issuer `orbit-auth`, a non-empty subject, issued-at time,
  and required expiration. RSA keys smaller than 2048 bits are rejected.
- The private key stays in Orbit Auth. Consumers can verify tokens but cannot
  mint them. This package does not own refresh tokens, product roles, or
  permission policy.

## Organization claim

An access token MAY carry an optional `org` claim: the UUID of the
organization the user selected as active ([ADR-045 D3](/decisions/045-tenancy-and-access)).

- `SignAccessToken` mints a token without the claim. `SignAccessTokenWithOrg`
  mints one with it and rejects an org that is not a UUID.
- The claim is omitted when no organization is active, so tokens minted before
  the claim existed keep verifying. Consumers must treat a missing `org` as
  "no active organization".
- `AccessClaims.OrganizationID` parses the claim as `uuid.UUID`. It returns
  `ok=false` when the claim is absent, blank or malformed; consumers must not
  treat a malformed claim as an authorization decision.
- Refreshing keeps the remembered organization while the user still has an
  active membership. If the membership was suspended or removed, the refreshed
  pair is issued without `org` and the refresh still succeeds.
- `AUTH_AUTO_SELECT_SINGLE_ORG` (default `true`) adds `org` at login only when
  the user has exactly one active membership. Zero or many memberships produce
  a token without `org`.
- Roles and permissions are never in the token. Products resolve them per
  request for the organization named by `org`.

## Key rotation

1. Generate a new RSA key pair and a unique key ID. Add the new public PEM as
   `<new-kid>.pem` to the public-key directory mounted by both Orbit Auth and
   the gateway. Keep the old public PEM in both directories. Restart both
   services while Auth still uses the old private key and active key ID. Check
   that the gateway's JWKS contains both key IDs.
2. Switch Orbit Auth to the new private PEM and `JWT_ACTIVE_KEY_ID`, then
   restart it. New tokens carry the new `kid`; product verifiers refresh JWKS
   when they encounter it.
3. Once every Auth instance has stopped signing with the old key, retain its
   public key for at least the maximum access-token lifetime, one verifier
   cache lifetime, and the clock-skew allowance. Then remove the old PEM from
   both public-key directories and restart Auth and the gateway.

The JWKS endpoint is public key material, but production consumers must fetch it
over HTTPS. Unknown keys fail closed when refresh fails. Known cached keys can
be used for up to one hour during a JWKS endpoint outage.
