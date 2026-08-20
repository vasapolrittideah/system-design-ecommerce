# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [ecommerce/common/v1/money.proto](#ecommerce_common_v1_money-proto)
    - [Money](#ecommerce-common-v1-Money)
  
- [ecommerce/catalog/v1/product.proto](#ecommerce_catalog_v1_product-proto)
    - [Product](#ecommerce-catalog-v1-Product)
    - [Variant](#ecommerce-catalog-v1-Variant)
    - [Variant.AttributesEntry](#ecommerce-catalog-v1-Variant-AttributesEntry)
  
    - [ProductStatus](#ecommerce-catalog-v1-ProductStatus)
  
- [ecommerce/catalog/v1/catalog_service.proto](#ecommerce_catalog_v1_catalog_service-proto)
    - [AddVariantRequest](#ecommerce-catalog-v1-AddVariantRequest)
    - [AddVariantResponse](#ecommerce-catalog-v1-AddVariantResponse)
    - [ArchiveProductRequest](#ecommerce-catalog-v1-ArchiveProductRequest)
    - [ArchiveProductResponse](#ecommerce-catalog-v1-ArchiveProductResponse)
    - [CreateProductRequest](#ecommerce-catalog-v1-CreateProductRequest)
    - [CreateProductResponse](#ecommerce-catalog-v1-CreateProductResponse)
    - [GetProductRequest](#ecommerce-catalog-v1-GetProductRequest)
    - [GetProductResponse](#ecommerce-catalog-v1-GetProductResponse)
    - [GetProductsByIDsRequest](#ecommerce-catalog-v1-GetProductsByIDsRequest)
    - [GetProductsByIDsResponse](#ecommerce-catalog-v1-GetProductsByIDsResponse)
    - [ListProductsRequest](#ecommerce-catalog-v1-ListProductsRequest)
    - [ListProductsResponse](#ecommerce-catalog-v1-ListProductsResponse)
    - [NewVariant](#ecommerce-catalog-v1-NewVariant)
    - [NewVariant.AttributesEntry](#ecommerce-catalog-v1-NewVariant-AttributesEntry)
    - [PublishProductRequest](#ecommerce-catalog-v1-PublishProductRequest)
    - [PublishProductResponse](#ecommerce-catalog-v1-PublishProductResponse)
    - [UpdateProductRequest](#ecommerce-catalog-v1-UpdateProductRequest)
    - [UpdateProductResponse](#ecommerce-catalog-v1-UpdateProductResponse)
    - [UpdateVariantRequest](#ecommerce-catalog-v1-UpdateVariantRequest)
    - [UpdateVariantRequest.AttributesEntry](#ecommerce-catalog-v1-UpdateVariantRequest-AttributesEntry)
    - [UpdateVariantResponse](#ecommerce-catalog-v1-UpdateVariantResponse)
  
    - [CatalogService](#ecommerce-catalog-v1-CatalogService)
  
- [ecommerce/identity/v1/user.proto](#ecommerce_identity_v1_user-proto)
    - [User](#ecommerce-identity-v1-User)
  
- [ecommerce/identity/v1/identity_service.proto](#ecommerce_identity_v1_identity_service-proto)
    - [GetUserRequest](#ecommerce-identity-v1-GetUserRequest)
    - [GetUserResponse](#ecommerce-identity-v1-GetUserResponse)
    - [GetUsersByIDsRequest](#ecommerce-identity-v1-GetUsersByIDsRequest)
    - [GetUsersByIDsResponse](#ecommerce-identity-v1-GetUsersByIDsResponse)
    - [LoginRequest](#ecommerce-identity-v1-LoginRequest)
    - [LoginResponse](#ecommerce-identity-v1-LoginResponse)
    - [LogoutRequest](#ecommerce-identity-v1-LogoutRequest)
    - [LogoutResponse](#ecommerce-identity-v1-LogoutResponse)
    - [RefreshTokenRequest](#ecommerce-identity-v1-RefreshTokenRequest)
    - [RefreshTokenResponse](#ecommerce-identity-v1-RefreshTokenResponse)
    - [RegisterRequest](#ecommerce-identity-v1-RegisterRequest)
    - [RegisterResponse](#ecommerce-identity-v1-RegisterResponse)
    - [TokenPair](#ecommerce-identity-v1-TokenPair)
  
    - [IdentityService](#ecommerce-identity-v1-IdentityService)
  
- [ecommerce/inventory/v1/stock.proto](#ecommerce_inventory_v1_stock-proto)
    - [Reservation](#ecommerce-inventory-v1-Reservation)
    - [ReservationLine](#ecommerce-inventory-v1-ReservationLine)
    - [StockItem](#ecommerce-inventory-v1-StockItem)
  
    - [ReservationStatus](#ecommerce-inventory-v1-ReservationStatus)
  
- [ecommerce/inventory/v1/inventory_service.proto](#ecommerce_inventory_v1_inventory_service-proto)
    - [AdjustStockRequest](#ecommerce-inventory-v1-AdjustStockRequest)
    - [AdjustStockResponse](#ecommerce-inventory-v1-AdjustStockResponse)
    - [CommitReservationRequest](#ecommerce-inventory-v1-CommitReservationRequest)
    - [CommitReservationResponse](#ecommerce-inventory-v1-CommitReservationResponse)
    - [CreateStockItemRequest](#ecommerce-inventory-v1-CreateStockItemRequest)
    - [CreateStockItemResponse](#ecommerce-inventory-v1-CreateStockItemResponse)
    - [GetReservationRequest](#ecommerce-inventory-v1-GetReservationRequest)
    - [GetReservationResponse](#ecommerce-inventory-v1-GetReservationResponse)
    - [GetStockBySKUsRequest](#ecommerce-inventory-v1-GetStockBySKUsRequest)
    - [GetStockBySKUsResponse](#ecommerce-inventory-v1-GetStockBySKUsResponse)
    - [NewReservationLine](#ecommerce-inventory-v1-NewReservationLine)
    - [ReleaseReservationRequest](#ecommerce-inventory-v1-ReleaseReservationRequest)
    - [ReleaseReservationResponse](#ecommerce-inventory-v1-ReleaseReservationResponse)
    - [ReserveStockRequest](#ecommerce-inventory-v1-ReserveStockRequest)
    - [ReserveStockResponse](#ecommerce-inventory-v1-ReserveStockResponse)
  
    - [InventoryService](#ecommerce-inventory-v1-InventoryService)
  
- [Scalar Value Types](#scalar-value-types)



<a name="ecommerce_common_v1_money-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/common/v1/money.proto



<a name="ecommerce-common-v1-Money"></a>

### Money
Money is an amount in one currency.

It lives here because a price quoted by catalog becomes a cart line and then
an order line without changing meaning, and three services inventing three
shapes for it is how a total ends up disagreeing with the sum of its parts.
This is the wire shape only — each service still owns a Money in its own
domain package, since the rules that protect an amount are the service&#39;s and
a domain that imported this file would be importing generated code.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| amount_minor | [int64](#int64) |  | The amount in the currency&#39;s minor unit: 1050 is THB 10.50, and 1050 in JPY is ¥1050 because yen has no minor unit — the number of decimal places is a property of currency_code, not of this field.

An integer rather than a decimal string because every arithmetic this system performs on a price is exact in minor units, and because a string would put a parse step between every service and the number. Signed because a discount and a refund are amounts too.

No bound is declared here. &#34;A price is not negative&#34; is a rule about prices, which catalog&#39;s domain holds; Money is also the shape of an adjustment, and a constraint on the message would forbid those everywhere it is reused. |
| currency_code | [string](#string) |  | ISO 4217, uppercase — &#34;THB&#34;, &#34;USD&#34;. Checked structurally here; whether this system actually sells in that currency is a question no shared message can answer. |





 

 

 

 



<a name="ecommerce_catalog_v1_product-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/catalog/v1/product.proto



<a name="ecommerce-catalog-v1-Product"></a>

### Product
Product is one thing on sale as the storefront describes it: the name, the
words, the category. It is never the thing that gets bought — that is a
Variant, and every service downstream of this one references those.

The outward shape, not the aggregate: the optimistic-locking version stays
inside the service, as does anything a future admin console needs and a
shopper does not.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| name | [string](#string) |  |  |
| description | [string](#string) |  |  |
| category | [string](#string) |  | A slug — &#34;clothing&#34;, &#34;clothing/shirts&#34;. Deliberately a string and not a Category message: nothing here needs a tree, localised category names, or a category that can be renamed in one place, and a taxonomy aggregate brings its own CRUD surface with it. It becomes an aggregate the day one of those is actually wanted, which is a field replaced rather than a service reshaped. |
| status | [ProductStatus](#ecommerce-catalog-v1-ProductStatus) |  |  |
| variants | [Variant](#ecommerce-catalog-v1-Variant) | repeated | Every variant of this product, including ones a shopper cannot buy today. Filtering is the caller&#39;s, because &#34;buyable&#34; means something different on a product page and in an admin list. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-catalog-v1-Variant"></a>

### Variant
Variant is the unit that is actually sold: one SKU, one price.

The split from Product exists even where a product will only ever have one
variant, because cart, inventory, and order all reference the sellable unit.
Adding this level of indirection later would be a breaking change in three
contracts and a migration in three databases; carrying it now costs one
table.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| product_id | [string](#string) |  |  |
| sku | [string](#string) |  | Unique across the service, uppercase. This is what the warehouse and the rest of the system call this thing, so it is chosen by whoever creates the product rather than generated here. |
| price | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  |  |
| attributes | [Variant.AttributesEntry](#ecommerce-catalog-v1-Variant-AttributesEntry) | repeated | What distinguishes this variant from its siblings — {&#34;size&#34;: &#34;M&#34;}, {&#34;weight_g&#34;: &#34;500&#34;}. Opaque on purpose: no rule in this service reads a key, which is exactly why the catalog can sell shirts and rice without knowing which it is doing. The day a rule does read one, that key is a field and not an entry here. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-catalog-v1-Variant-AttributesEntry"></a>

### Variant.AttributesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [string](#string) |  |  |





 


<a name="ecommerce-catalog-v1-ProductStatus"></a>

### ProductStatus
ProductStatus is where a product sits in its lifecycle. The transitions
between these are the catalog&#39;s state machine and belong to its domain;
this enum only names the states so a caller can read them.

| Name | Number | Description |
| ---- | ------ | ----------- |
| PRODUCT_STATUS_UNSPECIFIED | 0 |  |
| PRODUCT_STATUS_DRAFT | 1 | Being written. Never shown to a shopper and never buyable. |
| PRODUCT_STATUS_ACTIVE | 2 | Published and buyable. |
| PRODUCT_STATUS_ARCHIVED | 3 | Withdrawn from sale, and kept forever rather than deleted: orders placed last year still name these variants, and a row a completed order points at must not disappear. |


 

 

 



<a name="ecommerce_catalog_v1_catalog_service-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/catalog/v1/catalog_service.proto



<a name="ecommerce-catalog-v1-AddVariantRequest"></a>

### AddVariantRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product_id | [string](#string) |  |  |
| variant | [NewVariant](#ecommerce-catalog-v1-NewVariant) |  |  |






<a name="ecommerce-catalog-v1-AddVariantResponse"></a>

### AddVariantResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  | The whole product, not the one variant. Adding a variant is a change to the product, and a caller that has to merge a returned fragment into a copy it already holds will eventually merge it wrong. |






<a name="ecommerce-catalog-v1-ArchiveProductRequest"></a>

### ArchiveProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-ArchiveProductResponse"></a>

### ArchiveProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-CreateProductRequest"></a>

### CreateProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| description | [string](#string) |  |  |
| category | [string](#string) |  |  |
| variants | [NewVariant](#ecommerce-catalog-v1-NewVariant) | repeated | May be empty: a draft is allowed to exist before anyone has decided what it costs. Publishing is where that stops being allowed. |






<a name="ecommerce-catalog-v1-CreateProductResponse"></a>

### CreateProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-GetProductRequest"></a>

### GetProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| status | [ProductStatus](#ecommerce-catalog-v1-ProductStatus) |  | Unset means ACTIVE only, exactly as it does on a listing.

A read by id has to be told, where a listing could have been left to its own default: there is no page to scope it and no filter a caller forgot, only an id somebody already has. Answering with whatever state that id is in would put an unfinished draft on a storefront for anyone who guessed a UUID, and the first sign of it would be the draft on a screen. |






<a name="ecommerce-catalog-v1-GetProductResponse"></a>

### GetProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-GetProductsByIDsRequest"></a>

### GetProductsByIDsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| ids | [string](#string) | repeated | Bounded because the response is: an unbounded id list is how a batch read drives its own callee out of memory. |






<a name="ecommerce-catalog-v1-GetProductsByIDsResponse"></a>

### GetProductsByIDsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| products | [Product](#ecommerce-catalog-v1-Product) | repeated | Products that exist, in no guaranteed order and possibly fewer than were asked for. A missing id is not an error: one archived product must not fail the whole screen a BFF is assembling. |






<a name="ecommerce-catalog-v1-ListProductsRequest"></a>

### ListProductsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| category | [string](#string) |  | Exact match on the slug, or every category when empty. |
| status | [ProductStatus](#ecommerce-catalog-v1-ProductStatus) |  | Unset means ACTIVE only.

The default is a filter rather than &#34;everything&#34; because the storefront is the caller, and of the two ways to be wrong here, showing a shopper an unfinished draft is the one nobody notices until it is on a screen. |
| page_size | [int32](#int32) |  | 0 means 20. |
| page_token | [string](#string) |  | next_page_token from the previous response, opaque to the caller. A cursor and not an offset: rows are inserted while a shopper pages, and an offset would skip or repeat whatever moved across the boundary. |






<a name="ecommerce-catalog-v1-ListProductsResponse"></a>

### ListProductsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| products | [Product](#ecommerce-catalog-v1-Product) | repeated |  |
| next_page_token | [string](#string) |  | Empty on the last page. |






<a name="ecommerce-catalog-v1-NewVariant"></a>

### NewVariant
NewVariant is a variant that does not exist yet — the fields a caller
supplies, without the id and timestamps the service assigns. A separate
message from Variant so that neither has fields the other must ignore.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  | Uppercase, so uniqueness never depends on how it was typed. Chosen by the caller rather than generated here because a SKU is what the warehouse already calls this thing. |
| price | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  |  |
| attributes | [NewVariant.AttributesEntry](#ecommerce-catalog-v1-NewVariant-AttributesEntry) | repeated |  |






<a name="ecommerce-catalog-v1-NewVariant-AttributesEntry"></a>

### NewVariant.AttributesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-PublishProductRequest"></a>

### PublishProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-PublishProductResponse"></a>

### PublishProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-UpdateProductRequest"></a>

### UpdateProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| name | [string](#string) |  |  |
| description | [string](#string) |  |  |
| category | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-UpdateProductResponse"></a>

### UpdateProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-UpdateVariantRequest"></a>

### UpdateVariantRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product_id | [string](#string) |  | The aggregate root, named even though variant_id alone would find it: a variant is only ever modified as part of its product, and passing both means a caller cannot ask this service to reprice something under a product it was not looking at. |
| variant_id | [string](#string) |  |  |
| price | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  |  |
| attributes | [UpdateVariantRequest.AttributesEntry](#ecommerce-catalog-v1-UpdateVariantRequest-AttributesEntry) | repeated |  |






<a name="ecommerce-catalog-v1-UpdateVariantRequest-AttributesEntry"></a>

### UpdateVariantRequest.AttributesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-UpdateVariantResponse"></a>

### UpdateVariantResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |





 

 

 


<a name="ecommerce-catalog-v1-CatalogService"></a>

### CatalogService
CatalogService owns products, their variants, and the prices on them. It is
reached over east-west gRPC only.

It owns no stock. What is on the shelf is written far more often than it is
described, and the rule that protects it — never sell the same unit twice —
cannot be enforced without reservations and expiries, which are a different
aggregate with a different vocabulary. That service arrives with the order
flow; this one answers what a thing is and what it costs.

Every field constraint below is declared here rather than checked in a
handler: a single interceptor enforces them, so a handler that validates its
own input is a bug wherever it appears. What is *not* here is anything that
needs the aggregate to answer it — that a published product has at least one
variant, that an archived one takes no edits, that every variant of one
product is priced in one currency. Those are invariants, they live in the
domain, and a constraint here could only ever repeat the easy half of them.

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| GetProduct | [GetProductRequest](#ecommerce-catalog-v1-GetProductRequest) | [GetProductResponse](#ecommerce-catalog-v1-GetProductResponse) | GetProduct reads one product with its variants, in the state the caller asked for and not found in any other. A storefront that names no state is answered about published products alone. |
| GetProductsByIDs | [GetProductsByIDsRequest](#ecommerce-catalog-v1-GetProductsByIDsRequest) | [GetProductsByIDsResponse](#ecommerce-catalog-v1-GetProductsByIDsResponse) | GetProductsByIDs reads many. Every service exposes one of these so a BFF can fill a screen without looping single-item calls. |
| ListProducts | [ListProductsRequest](#ecommerce-catalog-v1-ListProductsRequest) | [ListProductsResponse](#ecommerce-catalog-v1-ListProductsResponse) | ListProducts pages through the catalog, newest first. |
| CreateProduct | [CreateProductRequest](#ecommerce-catalog-v1-CreateProductRequest) | [CreateProductResponse](#ecommerce-catalog-v1-CreateProductResponse) | CreateProduct creates a product, in draft, optionally with variants.

Deliberately not named Get* or Batch*: the client retry policy reads method names to decide what is safe to retry, and a retried create is a second product. |
| UpdateProduct | [UpdateProductRequest](#ecommerce-catalog-v1-UpdateProductRequest) | [UpdateProductResponse](#ecommerce-catalog-v1-UpdateProductResponse) | UpdateProduct replaces the descriptive fields of a product.

Wholesale rather than a patch: with three fields, a field mask buys nothing but a second way to be wrong about which of them the caller meant to clear. A caller sends what the product should now say. |
| AddVariant | [AddVariantRequest](#ecommerce-catalog-v1-AddVariantRequest) | [AddVariantResponse](#ecommerce-catalog-v1-AddVariantResponse) | AddVariant adds a sellable unit to a product. |
| UpdateVariant | [UpdateVariantRequest](#ecommerce-catalog-v1-UpdateVariantRequest) | [UpdateVariantResponse](#ecommerce-catalog-v1-UpdateVariantResponse) | UpdateVariant reprices a variant or restates what distinguishes it.

The SKU is not among the fields it can change. Orders, carts, and the warehouse all refer to a variant by that string, so renaming one would rename a thing other systems have already written down. |
| PublishProduct | [PublishProductRequest](#ecommerce-catalog-v1-PublishProductRequest) | [PublishProductResponse](#ecommerce-catalog-v1-PublishProductResponse) | PublishProduct moves a draft into the storefront. |
| ArchiveProduct | [ArchiveProductRequest](#ecommerce-catalog-v1-ArchiveProductRequest) | [ArchiveProductResponse](#ecommerce-catalog-v1-ArchiveProductResponse) | ArchiveProduct withdraws a product from sale. Nothing is deleted, and the move is one-way — a product that should sell again is a new one, because reviving an archived product silently revives whatever was wrong with it. |

 



<a name="ecommerce_identity_v1_user-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/identity/v1/user.proto



<a name="ecommerce-identity-v1-User"></a>

### User
User is what the identity service tells everyone else about a person.

It is the outward shape, not the aggregate: the password hash, the refresh
tokens, and the optimistic-locking version stay inside the service and never
appear here. A field on this message is a promise to every caller, so it is
far cheaper to add one later than to take one back.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  | Stable identifier, a UUID. It is also the &#34;sub&#34; claim of every access token issued for this user, so nothing else may be used to identify them. |
| email | [string](#string) |  | Login identity, unique across the service and stored lowercased. |
| roles | [string](#string) | repeated | What the user is, never what they may do with a particular aggregate — that rule belongs to whichever service owns the aggregate. These are the roles copied into the &#34;roles&#34; claim at sign time. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |





 

 

 

 



<a name="ecommerce_identity_v1_identity_service-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/identity/v1/identity_service.proto



<a name="ecommerce-identity-v1-GetUserRequest"></a>

### GetUserRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-identity-v1-GetUserResponse"></a>

### GetUserResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| user | [User](#ecommerce-identity-v1-User) |  |  |






<a name="ecommerce-identity-v1-GetUsersByIDsRequest"></a>

### GetUsersByIDsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| ids | [string](#string) | repeated | Bounded because the response is: an unbounded id list is how a batch read drives its own callee out of memory. |






<a name="ecommerce-identity-v1-GetUsersByIDsResponse"></a>

### GetUsersByIDsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| users | [User](#ecommerce-identity-v1-User) | repeated | Users present in the store, in no guaranteed order and possibly fewer than were asked for. A missing id is not an error: one deleted user must not fail the whole screen the Composition API is assembling. |






<a name="ecommerce-identity-v1-LoginRequest"></a>

### LoginRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| email | [string](#string) |  |  |
| password | [string](#string) |  | Bounded, but with no minimum length beyond one byte: the rule the caller has to satisfy is whatever their password already is, and a policy tightened after they registered must not lock them out of an account they can still open. The upper bound is here for the same reason as on RegisterRequest — the value is handed to a deliberately slow hash. |






<a name="ecommerce-identity-v1-LoginResponse"></a>

### LoginResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tokens | [TokenPair](#ecommerce-identity-v1-TokenPair) |  |  |
| user | [User](#ecommerce-identity-v1-User) |  | The user, so the screen that just signed them in does not immediately ask for what this call already loaded. Unlike RegisterResponse, which returns no tokens, there is nothing to separate here: the caller proved who they are one field ago. |






<a name="ecommerce-identity-v1-LogoutRequest"></a>

### LogoutRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| refresh_token | [string](#string) |  |  |






<a name="ecommerce-identity-v1-LogoutResponse"></a>

### LogoutResponse
LogoutResponse is empty, and is a message rather than google.protobuf.Empty
so that the day it carries something — how many sessions were revoked, say —
is a field added rather than a signature changed.






<a name="ecommerce-identity-v1-RefreshTokenRequest"></a>

### RefreshTokenRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| refresh_token | [string](#string) |  | The opaque string from a previous TokenPair. The bound is generous rather than exact so the encoding can change without a contract change; what matters is that an unbounded string never reaches a hash function. |






<a name="ecommerce-identity-v1-RefreshTokenResponse"></a>

### RefreshTokenResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tokens | [TokenPair](#ecommerce-identity-v1-TokenPair) |  | A new pair every time, including a new refresh token. The one presented is spent by the time this returns. |






<a name="ecommerce-identity-v1-RegisterRequest"></a>

### RegisterRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| email | [string](#string) |  | Bounded at 254 bytes, the longest address SMTP is required to accept. |
| password | [string](#string) |  | Length is all that is checked here. Composition rules (&#34;one digit, one symbol&#34;) push users toward predictable passwords and belong nowhere near a transport contract.

The upper bound exists so an unbounded input cannot be handed to a deliberately slow hash. If bcrypt is ever chosen over argon2id it must drop to 72: bcrypt silently truncates there, which would make two different long passwords authenticate each other. |






<a name="ecommerce-identity-v1-RegisterResponse"></a>

### RegisterResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| user | [User](#ecommerce-identity-v1-User) |  | The created user, and no tokens. Registering and signing in are separate acts — bundling them means a client that only wanted to create an account is handed a credential it has to decide what to do with, and it hides the case where registration succeeds but sign-in should not be allowed yet. |






<a name="ecommerce-identity-v1-TokenPair"></a>

### TokenPair
TokenPair is what a successful sign-in or refresh hands back.

One message shared by both responses, so a field added for one is present on
the other. A client that has to read two shapes to learn the same fact ends
up with two code paths that drift.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| access_token | [string](#string) |  | A signed ES256 JWT, carried as the Authorization bearer token on every subsequent request and verified by Kong and the Composition API without either of them asking this service. That is the point of the asymmetry: only identity holds the private key, and everything else holds a key that can say no. |
| refresh_token | [string](#string) |  | An opaque random string, deliberately not a JWT.

A refresh token has to be revocable, and a self-contained signed token cannot be — logout would have to wait out the TTL. This one means nothing on its own: the service stores a hash of it in a row, and deleting the row is what ends the session. |
| access_token_expires_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | When access_token stops being accepted. Absolute rather than a duration in seconds, because the token itself carries an absolute &#34;exp&#34; and a client comparing the two should not have to reconstruct one from the other. |
| refresh_token_expires_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | When refresh_token stops being accepted, after which the user signs in again. Rotation does not extend it: the chain has a fixed end, or a stolen token that is refreshed often enough would never expire. |





 

 

 


<a name="ecommerce-identity-v1-IdentityService"></a>

### IdentityService
IdentityService owns users, their credentials, and the tokens minted from
them. It is reached over east-west gRPC only — the edge is Kong and the
Composition API, and no service in this repo speaks HTTP.

Every field constraint below is declared here rather than checked in a
handler: a single interceptor enforces them, so a handler that validates its
own input is a bug wherever it appears.

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| Register | [RegisterRequest](#ecommerce-identity-v1-RegisterRequest) | [RegisterResponse](#ecommerce-identity-v1-RegisterResponse) | Register creates a user from an email and a password.

Deliberately not named Create*, and deliberately not a read: the client retry policy treats Get/List/Batch/Search/Count/Check as idempotent and retries them on Unavailable, which would turn one slow registration into several accounts. |
| GetUser | [GetUserRequest](#ecommerce-identity-v1-GetUserRequest) | [GetUserResponse](#ecommerce-identity-v1-GetUserResponse) | GetUser reads one user by ID. |
| GetUsersByIDs | [GetUsersByIDsRequest](#ecommerce-identity-v1-GetUsersByIDsRequest) | [GetUsersByIDsResponse](#ecommerce-identity-v1-GetUsersByIDsResponse) | GetUsersByIDs reads many. Every service exposes one of these so the Composition API can fill a screen without looping single-item calls. |
| Login | [LoginRequest](#ecommerce-identity-v1-LoginRequest) | [LoginResponse](#ecommerce-identity-v1-LoginResponse) | Login exchanges credentials for a token pair.

Named for the act rather than for the read it resembles: the client retry policy treats Get/List/Batch/Search/Count/Check as idempotent, and every attempt here mints a refresh token that is stored, so a retried Login would leave rows nobody holds.

A wrong password and an address that was never registered are answered identically — Unauthenticated with INVALID_CREDENTIALS — because any difference between them turns this RPC into a way to ask which email addresses have accounts. |
| RefreshToken | [RefreshTokenRequest](#ecommerce-identity-v1-RefreshTokenRequest) | [RefreshTokenResponse](#ecommerce-identity-v1-RefreshTokenResponse) | RefreshToken exchanges a refresh token for a new pair and invalidates the one presented.

Rotation on every call is what makes a stolen refresh token detectable: the thief and the legitimate client cannot both keep using the chain, and whichever presents the spent token second reveals that a copy exists. The service then revokes the whole chain, so the answer to a theft is that both parties have to sign in again — which the real user can do and the thief cannot.

That detection is also why this RPC is not idempotent and must never be retried automatically: a retry presents a token the first attempt already spent, which is indistinguishable from the theft it is designed to catch. |
| Logout | [LogoutRequest](#ecommerce-identity-v1-LogoutRequest) | [LogoutResponse](#ecommerce-identity-v1-LogoutResponse) | Logout revokes the presented refresh token and the rest of its rotation chain, so no further pair can be minted from it.

It cannot revoke the access token already in the caller&#39;s hands — a self-contained signed token is valid until it expires, and that window is the whole reason the TTL is 15 minutes. A screen that must stop working immediately has to ask the owning service, not the token.

Revoking something already revoked, expired, or never issued succeeds. Logging out is a state the caller wants to reach rather than a change they are making, a client retrying after a timeout must not see a failure, and an error here would tell a prober which tokens exist. |

 



<a name="ecommerce_inventory_v1_stock-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/inventory/v1/stock.proto



<a name="ecommerce-inventory-v1-Reservation"></a>

### Reservation
Reservation is one order&#39;s claim on stock, held for a while and then either
committed or given back.

The whole order is one reservation rather than one per line, because it is
released and committed as a unit: a saga that compensated line by line could
leave half an order holding capacity nobody will ever buy.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| order_id | [string](#string) |  | The order this was taken for. Unique, which is what makes ReserveStock idempotent — a saga that retries after an ambiguous timeout gets the same reservation back rather than a second hold on the same goods. |
| lines | [ReservationLine](#ecommerce-inventory-v1-ReservationLine) | repeated |  |
| status | [ReservationStatus](#ecommerce-inventory-v1-ReservationStatus) |  |  |
| expires_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | When a held reservation stops being honoured. Past this the capacity is the reaper&#39;s to return, and CommitReservation refuses — see the RPC. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-inventory-v1-ReservationLine"></a>

### ReservationLine
ReservationLine is one SKU&#39;s share of a reservation.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| quantity | [int32](#int32) |  |  |






<a name="ecommerce-inventory-v1-StockItem"></a>

### StockItem
StockItem is how many of one sellable unit the warehouse has, split by what
is still sellable and what an order has already spoken for.

It is keyed by SKU rather than by variant id. The catalog mints the id, but
the SKU is what a shelf, a picking list, and a supplier&#39;s invoice all say, and
a service that counted physical things by a UUID would be unable to answer the
question a human actually asks.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| available | [int32](#int32) |  | What a new reservation may still take. |
| reserved | [int32](#int32) |  | What live reservations are holding. It becomes available again when a reservation is released or expires, and simply disappears when one is committed — the goods left the building. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |





 


<a name="ecommerce-inventory-v1-ReservationStatus"></a>

### ReservationStatus
ReservationStatus is where a reservation sits in its lifecycle. The
transitions between these are this service&#39;s state machine and belong to its
domain; this enum only names the states so a caller can read them.

| Name | Number | Description |
| ---- | ------ | ----------- |
| RESERVATION_STATUS_UNSPECIFIED | 0 |  |
| RESERVATION_STATUS_HELD | 1 | Holding capacity, and doing so until expires_at. |
| RESERVATION_STATUS_COMMITTED | 2 | The goods were sold. The held quantity is gone rather than returned. |
| RESERVATION_STATUS_RELEASED | 3 | Given back on purpose — the compensating step of a saga that failed further along. |
| RESERVATION_STATUS_EXPIRED | 4 | Given back by the reaper, because nobody committed it in time. Distinct from RELEASED so that a stranded saga is visible as such instead of looking like a compensation somebody ran. |


 

 

 



<a name="ecommerce_inventory_v1_inventory_service-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/inventory/v1/inventory_service.proto



<a name="ecommerce-inventory-v1-AdjustStockRequest"></a>

### AdjustStockRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| delta | [int32](#int32) |  | Non-zero, and either sign. An adjustment that would take available below zero is refused rather than clamped — the count is wrong either way, and clamping hides which. |
| reason | [string](#string) |  | Why the count moved, for the humans reading the log line. Free text and not an enum: no rule here reads it, and an enum would be a list to extend every time a warehouse invents a new way to lose a box. |






<a name="ecommerce-inventory-v1-AdjustStockResponse"></a>

### AdjustStockResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| item | [StockItem](#ecommerce-inventory-v1-StockItem) |  |  |






<a name="ecommerce-inventory-v1-CommitReservationRequest"></a>

### CommitReservationRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-inventory-v1-CommitReservationResponse"></a>

### CommitReservationResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| reservation | [Reservation](#ecommerce-inventory-v1-Reservation) |  |  |






<a name="ecommerce-inventory-v1-CreateStockItemRequest"></a>

### CreateStockItemRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| available | [int32](#int32) |  | May be zero: a SKU that is tracked but not yet delivered is an ordinary state, and it is the one that keeps an order from being taken for it. |






<a name="ecommerce-inventory-v1-CreateStockItemResponse"></a>

### CreateStockItemResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| item | [StockItem](#ecommerce-inventory-v1-StockItem) |  |  |






<a name="ecommerce-inventory-v1-GetReservationRequest"></a>

### GetReservationRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-inventory-v1-GetReservationResponse"></a>

### GetReservationResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| reservation | [Reservation](#ecommerce-inventory-v1-Reservation) |  |  |






<a name="ecommerce-inventory-v1-GetStockBySKUsRequest"></a>

### GetStockBySKUsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| skus | [string](#string) | repeated | Bounded because the response is: an unbounded list is how a batch read drives its own callee out of memory. |






<a name="ecommerce-inventory-v1-GetStockBySKUsResponse"></a>

### GetStockBySKUsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| items | [StockItem](#ecommerce-inventory-v1-StockItem) | repeated | The SKUs this service tracks, in no guaranteed order and possibly fewer than were asked for. A SKU nobody has stocked yet is not an error: one such line must not fail the whole screen a caller is assembling. |






<a name="ecommerce-inventory-v1-NewReservationLine"></a>

### NewReservationLine
NewReservationLine is one SKU a caller wants held.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| quantity | [int32](#int32) |  |  |






<a name="ecommerce-inventory-v1-ReleaseReservationRequest"></a>

### ReleaseReservationRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-inventory-v1-ReleaseReservationResponse"></a>

### ReleaseReservationResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| reservation | [Reservation](#ecommerce-inventory-v1-Reservation) |  |  |






<a name="ecommerce-inventory-v1-ReserveStockRequest"></a>

### ReserveStockRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| order_id | [string](#string) |  | The order taking the hold, and the idempotency key. A caller that has no order id yet has nothing to reserve against — the aggregate is persisted after this call returns, but its identifier is minted before. |
| lines | [NewReservationLine](#ecommerce-inventory-v1-NewReservationLine) | repeated | Two lines naming the same SKU are summed rather than refused: a cart that added the same shirt twice is describing one quantity, and the caller should not have to know that. |






<a name="ecommerce-inventory-v1-ReserveStockResponse"></a>

### ReserveStockResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| reservation | [Reservation](#ecommerce-inventory-v1-Reservation) |  |  |





 

 

 


<a name="ecommerce-inventory-v1-InventoryService"></a>

### InventoryService
InventoryService owns how many of each SKU exist and who has claimed them. It
is reached over east-west gRPC only.

It owns no prices and no descriptions. The catalog answers what a thing is and
what it costs; this service answers whether there is one left, which is written
far more often than it is read and is protected by a rule the catalog has no
way to enforce: never sell the same unit twice.

The one rule worth stating at this level is that reserving is synchronous. It
is the step an order takes before it persists anything, so that a shopper
learns about a sold-out SKU while they are still looking at the screen rather
than in an email ten seconds later.

Every field constraint below is declared here rather than checked in a
handler: a single interceptor enforces them, so a handler that validates its
own input is a bug wherever it appears.

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| GetStockBySKUs | [GetStockBySKUsRequest](#ecommerce-inventory-v1-GetStockBySKUsRequest) | [GetStockBySKUsResponse](#ecommerce-inventory-v1-GetStockBySKUsResponse) | GetStockBySKUs reads the counts for many SKUs. Every service exposes one of these so a caller can fill a screen without looping single-item calls. |
| GetReservation | [GetReservationRequest](#ecommerce-inventory-v1-GetReservationRequest) | [GetReservationResponse](#ecommerce-inventory-v1-GetReservationResponse) | GetReservation reads one reservation, which is how a saga that lost track of an ambiguous call finds out what actually happened. |
| CreateStockItem | [CreateStockItemRequest](#ecommerce-inventory-v1-CreateStockItemRequest) | [CreateStockItemResponse](#ecommerce-inventory-v1-CreateStockItemResponse) | CreateStockItem starts tracking a SKU, at a starting count.

Explicit rather than folded into AdjustStock as an upsert: a typo in a SKU would otherwise create stock for something nobody sells, and the first sign of it would be an order for goods that do not exist. |
| AdjustStock | [AdjustStockRequest](#ecommerce-inventory-v1-AdjustStockRequest) | [AdjustStockResponse](#ecommerce-inventory-v1-AdjustStockResponse) | AdjustStock moves the available count by a delta — a delivery arriving, a breakage written off.

A delta and not an absolute count, because two receipts landing at once would otherwise overwrite each other and the loss would be silent. It is named for a mutation on purpose: the client retry policy reads method names, and a retried adjustment is a second delivery. |
| ReserveStock | [ReserveStockRequest](#ecommerce-inventory-v1-ReserveStockRequest) | [ReserveStockResponse](#ecommerce-inventory-v1-ReserveStockResponse) | ReserveStock holds stock for an order, for as long as this service&#39;s configured TTL.

Idempotent on order_id rather than on the request as a whole: a saga that retries after an ambiguous timeout is asking whether its hold exists, and gets the existing reservation back unchanged. A *different* set of lines under an order_id that already holds one is a conflict, not a replacement.

Not enough stock is FailedPrecondition with reason OUT_OF_STOCK and the SKU in the metadata, so a caller can name the line that failed. It is deliberately not one of the codes a circuit breaker counts: a run of sold-out SKUs is this service working correctly. |
| CommitReservation | [CommitReservationRequest](#ecommerce-inventory-v1-CommitReservationRequest) | [CommitReservationResponse](#ecommerce-inventory-v1-CommitReservationResponse) | CommitReservation turns a hold into a sale. The held quantity leaves the warehouse rather than returning to available.

It refuses a reservation whose expires_at has passed, even while the reaper has not got to it yet: the capacity is already promised to whoever asks next, and committing on the strength of the reaper being slow is how the same unit gets sold twice. A saga that meets this reserves again or fails the order.

Committing one that is already committed succeeds and changes nothing — every step of a saga is retried eventually. |
| ReleaseReservation | [ReleaseReservationRequest](#ecommerce-inventory-v1-ReleaseReservationRequest) | [ReleaseReservationResponse](#ecommerce-inventory-v1-ReleaseReservationResponse) | ReleaseReservation gives the held stock back. This is the compensating step of a saga that failed after reserving, and it is idempotent for the same reason: compensation runs more than once. |

 



## Scalar Value Types

| .proto Type | Notes | C++ | Java | Python | Go | C# | PHP | Ruby |
| ----------- | ----- | --- | ---- | ------ | -- | -- | --- | ---- |
| <a name="double" /> double |  | double | double | float | float64 | double | float | Float |
| <a name="float" /> float |  | float | float | float | float32 | float | float | Float |
| <a name="int32" /> int32 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint32 instead. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="int64" /> int64 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint64 instead. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="uint32" /> uint32 | Uses variable-length encoding. | uint32 | int | int/long | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="uint64" /> uint64 | Uses variable-length encoding. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum or Fixnum (as required) |
| <a name="sint32" /> sint32 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int32s. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sint64" /> sint64 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int64s. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="fixed32" /> fixed32 | Always four bytes. More efficient than uint32 if values are often greater than 2^28. | uint32 | int | int | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="fixed64" /> fixed64 | Always eight bytes. More efficient than uint64 if values are often greater than 2^56. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum |
| <a name="sfixed32" /> sfixed32 | Always four bytes. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sfixed64" /> sfixed64 | Always eight bytes. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="bool" /> bool |  | bool | boolean | boolean | bool | bool | boolean | TrueClass/FalseClass |
| <a name="string" /> string | A string must always contain UTF-8 encoded or 7-bit ASCII text. | string | String | str/unicode | string | string | string | String (UTF-8) |
| <a name="bytes" /> bytes | May contain any arbitrary sequence of bytes. | string | ByteString | str | []byte | ByteString | string | String (ASCII-8BIT) |

