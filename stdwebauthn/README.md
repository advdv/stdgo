# stdwebauthn

## Example id tokens from providers
Note1: 	// Linkedin does not support "public" subject tokens. So if we need to rotate the Linkedin oidc client_id/secret
	// all our identities will change. Making it even more important that account (re)linking works well. As when this
	// happens all users logging in via linkedin should be able to link to their old profile.

Note 2: 
	// We need to turn the id token claims into a reliable identifier for our user. This identifier needs to be unique
	// for the user, and be immutable. Also it should given the same value even if the the client_id/client_secret are
	// switched out or rotated.

Note 3:
		// EmailVerified any `json:"email_verified"`
		// google encodes this as a bool, linkedin as a string. Microsoft does not return it at all.
		// https://learn.microsoft.com/en-us/answers/questions/812672/microsoft-openid-connect-getting-verified-email
		// some discussion about this here: https://github.com/ory/kratos/pull/433
		//
		// It seems common that for some providers the "email_verified" status is not trusted. And more of a "hint" anyway
		// https://github.com/IQSS/dataverse/issues/6679 https://github.com/keycloak/keycloak/discussions/8622
		// It seems to be OK to use this for sending transactional/marketing email but not for authentication decisions
		// such as account linking.
```
// ### Microsoft ###
    app 1: Sterndesk (Staging)
	{
		"ver": "2.0",
		"iss": "https://login.microsoftonline.com/9188040d-6c67-4c5b-b112-36a304b66dad/v2.0",
		"sub": "AAAAAAAAAAAAAAAAAAAAAJh6IifS_sEmInqbacAe5gw",
		"aud": "cdf06d77-368a-41fa-a66e-78410c60dcd7",
		"exp": 1747311281,
		"iat": 1747224581,
		"nbf": 1747224581,
		"name": "Ad van der Veer",
		"preferred_username": "advanderveer@gmail.com",
		"oid": "00000000-0000-0000-d856-474049a1aad0",
		"email": "advanderveer@gmail.com",
		"tid": "9188040d-6c67-4c5b-b112-36a304b66dad",
		"aio": "DnID4Chz!...*SvfniVhgJT22FEK0ZEWJkKdbOR"
	}
	app 2: Sterndesk
	{
		"ver": "2.0",
		"iss": "https://login.microsoftonline.com/9188040d-6c67-4c5b-b112-36a304b66dad/v2.0",
		"sub": "AAAAAAAAAAAAAAAAAAAAAC86BKg0Et16-VMCPEC8N0U",
		"aud": "7972410d-c841-4c82-a872-73afde4d3ef2",
		"exp": 1747311968,
		"iat": 1747225268,
		"nbf": 1747225268,
		"name": "Ad van der Veer",
		"preferred_username": "advanderveer@gmail.com",
		"oid": "00000000-0000-0000-d856-474049a1aad0",
		                           d856 474049a1aad0
		"email": "advanderveer@gmail.com",
		"tid": "9188040d-6c67-4c5b-b112-36a304b66dad",
		"aio": "DqnTG4fY1G2aCMh!E6GAHPF8CVJ....3G0xrb5jjSy0J8jDr2o95Kafjc"
	}
// ### LinkedIn ###
	app 1: Sterndesk (Staging)
	{
		"iss": "https://www.linkedin.com/oauth",
		"aud": "78mzfmuak6gfvj",
		"iat": 1747227271,
		"exp": 1747230871,
		"sub": "-oQzDrUKmr",
		"name": "Adam van der Veer",
		"given_name": "Adam",
		"family_name": "van der Veer",
		"picture": "https://media.lic,..t=5G95B3nCwQnrV4BCmRAWHt_7eZMx08jC_iq4tuEbqyc",
		"email": "advanderveer@gmail.com",
		"email_verified": "true",
		"locale": "en_US"
		}
	app 2: Sterndesk
	{
		"iss": "https://www.linkedin.com/oauth",
		"aud": "78nwvm0xvt7t0z",
		"iat": 1747227710,
		"exp": 1747231310,
		"sub": "cq7p3geg8a",
		"name": "Adam van der Veer",
		"given_name": "Adam",
		"family_name": "van der Veer",
		"picture": "https://media.licdn.co...5G95B3nCwQnrV4BCmRAWHt_7eZMx08jC_iq4tuEbqyc",
		"email": "advanderveer@gmail.com",
		"email_verified": "true",
		"locale": "en_US"
	}
```