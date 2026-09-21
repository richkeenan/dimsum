# Hand-encoded DNS fixtures

`rfc1035-a.hex` applies RFC 1035 §4.1.1–4.1.4 (header, question,
RR and name compression) to a synthetic documentation-only answer:

- ID `0x1234`, flags `0x8180` (QR, RD, RA), counts 1/1/0/0.
- At 12: `WWW.Example.COM.`, QTYPE A, QCLASS IN; question ends at 33.
- At 33: owner pointer `c00c`; A/IN; TTL 300 at 39; RDLENGTH 4 at 43.
- At 45: 192.0.2.1; message ends at 49.

This is hand-encoded, not a capture or output from a production/oracle builder.
The name tables also transcribe RFC 1035 §4.1.4's F.ISI.ARPA and
FOO.F.ISI.ARPA compression example (offsets rebased to zero).
EDNS tables apply RFC 6891 §6.1 and §6.2.3, with TLV payloads from test
inputs rather than live DNS. Binary-label and malformed fixtures have explicit
byte expectations independent of the oracle. Fuzz targets seed these tables.
