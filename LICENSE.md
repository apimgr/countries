MIT License

Copyright (c) 2026 apimgr

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

---

## Third-Party Licenses

| Library | Version | License | Copyright |
|---|---:|---|---|
| github.com/aymerick/douceur | v0.2.0 | MIT | Aymerick and contributors |
| github.com/beorn7/perks | v1.0.1 | MIT | The Go Perks Authors |
| github.com/cespare/xxhash/v2 | v2.3.0 | MIT | Caleb Spare |
| github.com/dustin/go-humanize | v1.1.0 | MIT | Dustin Sallings |
| github.com/go-chi/chi/v5 | v5.3.0 | MIT | Peter Kieltyka and contributors |
| github.com/graph-gophers/graphql-go | v1.10.0 | BSD-2-Clause | GraphQL for Go contributors |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | Google Inc. |
| github.com/gorilla/css | v1.0.1 | BSD-3-Clause | The Gorilla Web Toolkit Authors |
| github.com/mattn/go-isatty | v0.0.24 | MIT | Yasuhiro Matsumoto |
| github.com/microcosm-cc/bluemonday | v1.0.27 | BSD-3-Clause | David Kitchen and contributors |
| github.com/ncruces/go-strftime | v1.1.0 | MIT | Nuno Cruces |
| github.com/oschwald/maxminddb-golang | v1.13.1 | ISC | Oliver Andrich |
| github.com/prometheus/client_golang | v1.25.0 | Apache-2.0 | The Prometheus Authors |
| github.com/prometheus/client_model | v0.6.3 | Apache-2.0 | The Prometheus Authors |
| github.com/prometheus/common | v0.72.0 | Apache-2.0 | The Prometheus Authors |
| github.com/prometheus/procfs | v0.22.0 | Apache-2.0 | The Prometheus Authors |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | The Go Authors |
| github.com/shirou/gopsutil/v3 | v3.24.5 | BSD-3-Clause | The gopsutil Authors |
| github.com/tklauser/go-sysconf | v0.3.12 | BSD-3-Clause | Tobias Klauser |
| github.com/tklauser/numcpus | v0.6.1 | Apache-2.0 | Tobias Klauser |
| github.com/munnerz/goautoneg | v0.0.0-20191010083416-a7dc8b61c822 | BSD-3-Clause | Munnerz and contributors |
| swagger-ui-dist | v5.34.1 | Apache-2.0 | SmartBear Software Inc. and contributors |
| github.com/yuin/goldmark | v1.8.7 | MIT | Yusuke Inuzuka |
| golang.org/x/crypto | v0.58.0 | BSD-3-Clause | The Go Authors |
| golang.org/x/net | v0.62.0 | BSD-3-Clause | The Go Authors |
| golang.org/x/sys | v0.49.0 | BSD-3-Clause | The Go Authors |
| golang.org/x/text | v0.43.0 | BSD-3-Clause | The Go Authors |
| google.golang.org/protobuf | v1.36.12 | BSD-3-Clause | The Go Authors |
| gopkg.in/yaml.v3 | v3.0.1 | MIT | Canonical Ltd. |
| modernc.org/libc | v1.79.0 | BSD-3-Clause | The Libc Authors |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause | The mathutil Authors |
| modernc.org/memory | v1.12.1 | BSD-3-Clause | The Memory Authors |
| modernc.org/sqlite | v1.61.1 | BSD-3-Clause | The Sqlite Authors |

BSD-3-Clause: neither the names of the copyright holders nor the names of their contributors may be used to endorse or promote products derived from this software without specific prior written permission.

## Third-Party Data

| Dataset | Source | License |
|---|---|---|
| IP geolocation city data | [DB-IP](https://db-ip.com/) | CC BY 4.0 |
| IP geolocation country and ASN data | [ip-location-db](https://github.com/sapics/ip-location-db) | PDDL |

The required visible attribution is: <a href="https://db-ip.com/">IP Geolocation by DB-IP</a>

## Embedded country data

The generated country database in `src/data/countries.json` is distributed under the Open Database License 1.0 (ODbL). It includes World Bank population values under CC BY 4.0, with attribution retained in [`src/data/README.md`](src/data/README.md). IANA tzdb data is public domain. These data licenses are separate from the MIT-licensed application code. Full license texts and field-level provenance are in `src/data/`.
