//revive:disable:var-naming
package yandex

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/iam/v1/awscompatibility"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/kms/v1"
)

func init() {
	resource.AddTestSweepers("yandex_storage_bucket", &resource.Sweeper{
		Name:         "yandex_storage_bucket",
		F:            testSweepStorageBucket,
		Dependencies: []string{},
	})
}

func testSweepStorageBucket(_ string) error {
	conf, err := configForSweepers()
	if err != nil {
		return fmt.Errorf("error getting client: %s", err)
	}

	result := &multierror.Error{}
	serviceAccountID, err := createIAMServiceAccountForSweeper(conf)
	if serviceAccountID != "" {
		defer func() {
			if !sweepIAMServiceAccount(conf, serviceAccountID) {
				result = multierror.Append(result,
					fmt.Errorf("failed to sweep IAM service account %q", serviceAccountID))
			}
		}()
	}
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("error creating service account: %s", err))
		return result.ErrorOrNil()
	}

	resp, err := conf.sdk.IAM().AWSCompatibility().AccessKey().Create(conf.Context(), &awscompatibility.CreateAccessKeyRequest{
		ServiceAccountId: serviceAccountID,
		Description:      "Storage Bucket sweeper static key",
	})
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("error creating service account static key: %s", err))
		return result.ErrorOrNil()
	}

	defer func() {
		_, err := conf.sdk.IAM().AWSCompatibility().AccessKey().Delete(conf.Context(), &awscompatibility.DeleteAccessKeyRequest{
			AccessKeyId: resp.AccessKey.Id,
		})
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("error deleting service account static key: %s", err))
		}
	}()

	s3client, err := getS3ClientByKeys(resp.AccessKey.KeyId, resp.Secret, conf)
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("error creating storage client: %s", err))
		return result.ErrorOrNil()
	}

	buckets, err := s3client.ListBuckets(&s3.ListBucketsInput{})
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("failed to list storage buckets: %s", err))
		return result.ErrorOrNil()
	}

	for _, b := range buckets.Buckets {
		_, err := s3client.DeleteBucket(&s3.DeleteBucketInput{
			Bucket: b.Name,
		})

		if err != nil {
			// ignore this error until we have sweeper for storage objects
			if err, ok := err.(awserr.Error); ok && err.Code() == "BucketNotEmpty" {
				continue
			}
			result = multierror.Append(result, fmt.Errorf("failed to delete bucket: %s, error: %s", *b.Name, err))
		}
	}

	return result.ErrorOrNil()
}

func TestAccStorageBucket_basic(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	resource.Test(t, resource.TestCase{
		PreCheck:      func() { testAccPreCheck(t) },
		IDRefreshName: resourceName,
		Providers:     testAccProviders,
		CheckDestroy:  testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfig(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					resource.TestCheckNoResourceAttr(resourceName, "website_endpoint"),
					resource.TestCheckResourceAttr(resourceName, "bucket", testAccBucketName(rInt)),
					resource.TestCheckResourceAttr(resourceName, "bucket_domain_name", testAccBucketDomainName(rInt)),
				),
			},
		},
	})
}

func TestAccStorageBucket_namePrefix(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	resource.Test(t, resource.TestCase{
		PreCheck:      func() { testAccPreCheck(t) },
		IDRefreshName: resourceName,
		Providers:     testAccProviders,
		CheckDestroy:  testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfigWithNamePrefix(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					resource.TestMatchResourceAttr(resourceName, "bucket", regexp.MustCompile("^tf-test-")),
				),
			},
		},
	})
}

func TestAccStorageBucket_generatedName(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	resource.Test(t, resource.TestCase{
		PreCheck:      func() { testAccPreCheck(t) },
		IDRefreshName: resourceName,
		Providers:     testAccProviders,
		CheckDestroy:  testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfigWithGeneratedName(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
				),
			},
		},
	})
}

func TestAccStorageBucket_updateAcl(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	resource.Test(t, resource.TestCase{
		PreCheck:        func() { testAccPreCheck(t) },
		IDRefreshName:   resourceName,
		IDRefreshIgnore: []string{"access_key", "secret_key"},
		Providers:       testAccProviders,
		CheckDestroy:    testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketAclPreConfig(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "acl", "public-read"),
				),
			},
			{
				Config: testAccStorageBucketAclPostConfig(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "acl", "private"),
				),
			},
		},
	})
}

func TestAccStorageBucket_website(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	resource.Test(t, resource.TestCase{
		PreCheck:        func() { testAccPreCheck(t) },
		IDRefreshName:   resourceName,
		IDRefreshIgnore: []string{"access_key", "secret_key"},
		Providers:       testAccProviders,
		CheckDestroy:    testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketWebsiteConfig(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					wrapWithRetries(testAccCheckStorageBucketWebsite(resourceName, "index.html", "", "", "")),
					resource.TestCheckResourceAttr(resourceName, "website.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "website.0.index_document", "index.html"),
					resource.TestCheckResourceAttr(resourceName, "website.0.error_document", ""),
					resource.TestCheckResourceAttr(resourceName, "website_endpoint", testAccWebsiteEndpoint(rInt)),
				),
			},
			{
				Config: testAccStorageBucketWebsiteConfigWithError(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					wrapWithRetries(testAccCheckStorageBucketWebsite(resourceName, "index.html", "error.html", "", "")),
					resource.TestCheckResourceAttr(resourceName, "website.0.index_document", "index.html"),
					resource.TestCheckResourceAttr(resourceName, "website.0.error_document", "error.html"),
					resource.TestCheckResourceAttr(resourceName, "website_endpoint", testAccWebsiteEndpoint(rInt)),
				),
			},
			{
				Config: testAccStorageBucketConfig(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					wrapWithRetries(testAccCheckStorageBucketWebsite(resourceName, "", "", "", "")),
					resource.TestCheckResourceAttr(resourceName, "website.#", "0"),
					resource.TestCheckResourceAttr(resourceName, "website_endpoint", ""),
				),
			},
		},
	})
}

// Test TestAccStorageBucket_shouldFailNotFound is designed to fail with a "plan
// not empty" error in Terraform, to check against regresssions.
// See https://github.com/hashicorp/terraform/pull/2925
func TestAccStorageBucket_shouldFailNotFound(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	resource.Test(t, resource.TestCase{
		PreCheck:      func() { testAccPreCheck(t) },
		IDRefreshName: "",
		Providers:     testAccProviders,
		CheckDestroy:  testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketDestroyedConfig(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					testAccCheckStorageDestroyBucket(resourceName),
					ensureBucketDeleted(resourceName),
				),
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccStorageBucket_cors_update(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	resource.Test(t, resource.TestCase{
		PreCheck:      func() { testAccPreCheck(t) },
		IDRefreshName: resourceName,
		Providers:     testAccProviders,
		CheckDestroy:  testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfigWithCORS(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					wrapWithRetries(testAccCheckStorageBucketCors(
						resourceName,
						[]*s3.CORSRule{
							{
								AllowedHeaders: []*string{aws.String("*")},
								AllowedMethods: []*string{aws.String("PUT"), aws.String("POST")},
								AllowedOrigins: []*string{aws.String("https://www.example.com")},
								ExposeHeaders:  []*string{aws.String("x-amz-server-side-encryption"), aws.String("ETag")},
								MaxAgeSeconds:  aws.Int64(3000),
							},
						},
					)),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_headers.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_headers.0", "*"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.0", "PUT"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.1", "POST"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_origins.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_origins.0", "https://www.example.com"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.max_age_seconds", "3000"),
				),
			},
			{
				Config: testAccStorageBucketConfigWithCORSUpdated(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					wrapWithRetries(testAccCheckStorageBucketCors(
						resourceName,
						[]*s3.CORSRule{
							{
								AllowedHeaders: []*string{aws.String("*")},
								AllowedMethods: []*string{aws.String("GET")},
								AllowedOrigins: []*string{aws.String("https://www.example.ru")},
								ExposeHeaders:  []*string{aws.String("x-amz-server-side-encryption"), aws.String("ETag")},
								MaxAgeSeconds:  aws.Int64(2000),
							},
						},
					)),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_headers.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_headers.0", "*"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.0", "GET"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_origins.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_origins.0", "https://www.example.ru"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.max_age_seconds", "2000"),
				),
			},
		},
	})
}

func TestAccStorageBucket_cors_delete(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	resource.Test(t, resource.TestCase{
		PreCheck:      func() { testAccPreCheck(t) },
		IDRefreshName: resourceName,
		Providers:     testAccProviders,
		CheckDestroy:  testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfigWithCORS(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					wrapWithRetries(testAccCheckStorageBucketCors(
						resourceName,
						[]*s3.CORSRule{
							{
								AllowedHeaders: []*string{aws.String("*")},
								AllowedMethods: []*string{aws.String("PUT"), aws.String("POST")},
								AllowedOrigins: []*string{aws.String("https://www.example.com")},
								ExposeHeaders:  []*string{aws.String("x-amz-server-side-encryption"), aws.String("ETag")},
								MaxAgeSeconds:  aws.Int64(3000),
							},
						},
					)),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_headers.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_headers.0", "*"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.0", "PUT"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.1", "POST"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_origins.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_origins.0", "https://www.example.com"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.max_age_seconds", "3000"),
				),
			},
			{
				Config: testAccStorageBucketConfig(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					wrapWithRetries(testAccCheckStorageBucketCors(resourceName, nil)),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.#", "0"),
				),
			},
		},
	})
}

func TestAccStorageBucket_cors_emptyOrigin(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	resource.Test(t, resource.TestCase{
		PreCheck:      func() { testAccPreCheck(t) },
		IDRefreshName: resourceName,
		Providers:     testAccProviders,
		CheckDestroy:  testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfigWithCORSEmptyOrigin(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					testAccCheckStorageBucketCors(resourceName,
						[]*s3.CORSRule{
							{
								AllowedHeaders: []*string{aws.String("*")},
								AllowedMethods: []*string{aws.String("PUT"), aws.String("POST")},
								AllowedOrigins: []*string{aws.String("")},
								ExposeHeaders:  []*string{aws.String("x-amz-server-side-encryption"), aws.String("ETag")},
								MaxAgeSeconds:  aws.Int64(3000),
							},
						},
					),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_headers.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_headers.0", "*"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.0", "PUT"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_methods.1", "POST"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_origins.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.allowed_origins.0", ""),
					resource.TestCheckResourceAttr(resourceName, "cors_rule.0.max_age_seconds", "3000"),
				),
			},
		},
	})
}

func TestAccStorageBucket_UpdateGrant(t *testing.T) {
	resourceName := "yandex_storage_bucket.test"
	userID := getExampleUserID2()
	ri := acctest.RandInt()
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfigWithGrants(ri, userID),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "grant.#", "1"),
					testAccCheckStorageBucketUpdateGrantSingle(resourceName, userID),
				),
			},
			{
				Config: testAccStorageBucketConfigWithGrantsUpdate(ri, userID),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "grant.#", "2"),
					testAccCheckStorageBucketUpdateGrantMulti(resourceName, userID),
				),
			},
			{
				Config: testAccStorageBucketBasic(ri),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "grant.#", "0"),
				),
			},
		},
	})
}

func TestAccStorageBucket_SSE(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"

	keyName := fmt.Sprintf("tf-test-%s", acctest.RandString(10))
	var symmetricKey kms.SymmetricKey

	resource.Test(t, resource.TestCase{
		PreCheck:      func() { testAccPreCheck(t) },
		IDRefreshName: resourceName,
		Providers:     testAccProviders,
		CheckDestroy:  testAccCheckStorageBucketSSEDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketSSEDefault(keyName, rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckKMSSymmetricKeyExists(
						"yandex_kms_symmetric_key.key-a", &symmetricKey),
					testAccCheckStorageBucketExists(resourceName),
					testAccCheckStorageBucketSSE(resourceName,
						&s3.ServerSideEncryptionConfiguration{
							Rules: []*s3.ServerSideEncryptionRule{
								{
									ApplyServerSideEncryptionByDefault: &s3.ServerSideEncryptionByDefault{
										KMSMasterKeyID: &symmetricKey.Id,
										SSEAlgorithm:   aws.String(s3.ServerSideEncryptionAwsKms),
									},
								},
							},
						},
					),
				),
			},
		},
	})
}

func TestStorageBucketName(t *testing.T) {
	validNames := []string{
		"foobar",
		"127.0.0.1",
		"foo..bar",
		"foo.bar.baz",
		"Foo.Bar",
		strings.Repeat("x", 63),
	}

	for _, v := range validNames {
		if err := validateS3BucketName(v); err != nil {
			t.Fatalf("%q should be a valid storage bucket name", v)
		}
	}

	invalidNames := []string{
		"foo_bar",
		"foo_bar_baz",
		"foo;bar",
		strings.Repeat("x", 64),
	}

	for _, v := range invalidNames {
		if err := validateS3BucketName(v); err == nil {
			t.Fatalf("%q should not be a valid storage bucket name", v)
		}
	}
}

func testAccCheckStorageBucketDestroy(s *terraform.State) error {
	return testAccCheckStorageBucketDestroyWithProvider(s, testAccProvider)
}

func testAccCheckStorageBucketSSEDestroy(s *terraform.State) error {
	err := testAccCheckStorageBucketDestroyWithProvider(s, testAccProvider)
	if err != nil {
		return err
	}
	return testAccCheckKMSSymmetricKeyDestroy(s)
}

func testAccCheckStorageBucketDestroyWithProvider(s *terraform.State, provider *schema.Provider) error {
	config := provider.Meta().(*Config)

	check := func(rs *terraform.ResourceState) error {
		// access and secret keys should be destroyed too and defaults may be not provided, so create temporary ones
		ak, sak, cleanup, err := createTemporaryStaticAccessKey("editor", config)
		if err != nil {
			return err
		}
		defer cleanup()

		conn, err := getS3ClientByKeys(ak, sak, config)
		if err != nil {
			return err
		}

		_, err = conn.DeleteBucket(&s3.DeleteBucketInput{
			Bucket: aws.String(rs.Primary.ID),
		})
		if err != nil {
			if isAWSErr(err, s3.ErrCodeNoSuchBucket, "") {
				return nil
			}
			return err
		}

		return nil
	}

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "yandex_storage_bucket" {
			continue
		}

		err := check(rs)
		if err != nil {
			return err
		}
	}
	return nil
}

func testAccCheckStorageBucketExists(n string) resource.TestCheckFunc {
	return testAccCheckStorageBucketExistsWithProvider(n, func() *schema.Provider { return testAccProvider })
}

func testAccCheckStorageBucketExistsWithProvider(n string, providerF func() *schema.Provider) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("no ID is set")
		}

		provider := providerF()

		conn, err := getS3ClientByKeys(rs.Primary.Attributes["access_key"], rs.Primary.Attributes["secret_key"],
			provider.Meta().(*Config))
		if err != nil {
			return err
		}

		_, err = conn.HeadBucket(&s3.HeadBucketInput{
			Bucket: aws.String(rs.Primary.ID),
		})
		if err != nil {
			if isAWSErr(err, s3.ErrCodeNoSuchBucket, "") {
				return fmt.Errorf("bucket not found")
			}
			return err
		}

		return nil
	}
}

func testAccCheckStorageDestroyBucket(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("no storage bucket ID is set")
		}

		conn, err := getS3ClientByKeys(rs.Primary.Attributes["access_key"], rs.Primary.Attributes["secret_key"],
			testAccProvider.Meta().(*Config))
		if err != nil {
			return err
		}

		_, err = conn.DeleteBucket(&s3.DeleteBucketInput{
			Bucket: aws.String(rs.Primary.ID),
		})
		if err != nil {
			return fmt.Errorf("error destroying bucket (%s) in testAccCheckStorageDestroyBucket: %s", rs.Primary.ID, err)
		}

		return nil
	}
}

func testAccCheckStorageBucketWebsite(n string, indexDoc string, errorDoc string, redirectProtocol string, redirectTo string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs := s.RootModule().Resources[n]
		conn, err := getS3ClientByKeys(rs.Primary.Attributes["access_key"], rs.Primary.Attributes["secret_key"],
			testAccProvider.Meta().(*Config))
		if err != nil {
			return err
		}

		out, err := conn.GetBucketWebsite(&s3.GetBucketWebsiteInput{
			Bucket: aws.String(rs.Primary.ID),
		})
		if err != nil {
			if indexDoc == "" {
				// If we want to assert that the website is not there, than
				// this error is expected
				return nil
			}

			return fmt.Errorf("S3BucketWebsite error: %v", err)
		}

		if v := out.IndexDocument; v == nil {
			if indexDoc != "" {
				return fmt.Errorf("bad index doc, found nil, expected: %s", indexDoc)
			}
		} else {
			if *v.Suffix != indexDoc {
				return fmt.Errorf("bad index doc, expected: %s, got %#v", indexDoc, out.IndexDocument)
			}
		}

		if v := out.ErrorDocument; v == nil {
			if errorDoc != "" {
				return fmt.Errorf("bad error doc, found nil, expected: %s", errorDoc)
			}
		} else {
			if *v.Key != errorDoc {
				return fmt.Errorf("bad error doc, expected: %s, got %#v", errorDoc, out.ErrorDocument)
			}
		}

		if v := out.RedirectAllRequestsTo; v == nil {
			if redirectTo != "" {
				return fmt.Errorf("bad redirect to, found nil, expected: %s", redirectTo)
			}
		} else {
			if *v.HostName != redirectTo {
				return fmt.Errorf("bad redirect to, expected: %s, got %#v", redirectTo, out.RedirectAllRequestsTo)
			}
			if redirectProtocol != "" && v.Protocol != nil && *v.Protocol != redirectProtocol {
				return fmt.Errorf("bad redirect protocol to, expected: %s, got %#v", redirectProtocol, out.RedirectAllRequestsTo)
			}
		}

		return nil
	}
}

func testAccCheckStorageBucketCors(n string, corsRules []*s3.CORSRule) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs := s.RootModule().Resources[n]
		conn, err := getS3ClientByKeys(rs.Primary.Attributes["access_key"], rs.Primary.Attributes["secret_key"],
			testAccProvider.Meta().(*Config))
		if err != nil {
			return err
		}

		out, err := conn.GetBucketCors(&s3.GetBucketCorsInput{
			Bucket: aws.String(rs.Primary.ID),
		})
		if err != nil && !isAWSErr(err, "NoSuchCORSConfiguration", "") {
			return fmt.Errorf("func GetBucketCors error: %v", err)
		}

		if !reflect.DeepEqual(out.CORSRules, corsRules) {
			return fmt.Errorf("bad error cors rule, expected: %v, got %v", corsRules, out.CORSRules)
		}

		return nil
	}
}

func testAccCheckStorageBucketSSE(n string, config *s3.ServerSideEncryptionConfiguration) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs := s.RootModule().Resources[n]
		conn, err := getS3ClientByKeys(rs.Primary.Attributes["access_key"], rs.Primary.Attributes["secret_key"],
			testAccProvider.Meta().(*Config))
		if err != nil {
			return err
		}

		out, err := conn.GetBucketEncryption(&s3.GetBucketEncryptionInput{
			Bucket: aws.String(rs.Primary.ID),
		})
		if err != nil && !isAWSErr(err, "NoSuchEncryptionConfiguration", "") {
			return fmt.Errorf("func GetBucketCors error: %v", err)
		}

		if !reflect.DeepEqual(out.ServerSideEncryptionConfiguration, config) {
			return fmt.Errorf("bad error cors rule, expected: %v, got %v", config, out.ServerSideEncryptionConfiguration)
		}

		return nil
	}
}

//// These need a bit of randomness as the name can only be used once globally
func testAccBucketName(randInt int) string {
	return fmt.Sprintf("tf-test-bucket-%d", randInt)
}

func testAccBucketDomainName(randInt int) string {
	name, _ := bucketDomainName(fmt.Sprintf("tf-test-bucket-%d", randInt), getExampleStorageEndpoint())
	return name
}

func testAccWebsiteEndpoint(randInt int) string {
	return fmt.Sprintf("tf-test-bucket-%d.%s", randInt, WebsiteDomainURL())
}

func testAccStorageBucketConfig(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	bucket = "tf-test-bucket-%[1]d"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key
}
`, randInt) + testAccCommonIamDependenciesEditorConfig(randInt)
}

func testAccStorageBucketAclPreConfig(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	bucket = "tf-test-bucket-%[1]d"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

	acl = "public-read"
}
`, randInt) + testAccCommonIamDependenciesAdminConfig(randInt)
}

func testAccStorageBucketAclPostConfig(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	bucket = "tf-test-bucket-%[1]d"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

	acl = "private"
}
`, randInt) + testAccCommonIamDependenciesAdminConfig(randInt)
}

func testAccStorageBucketWebsiteConfig(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	bucket = "tf-test-bucket-%[1]d"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

	website {
		index_document = "index.html"
	}
}
`, randInt) + testAccCommonIamDependenciesEditorConfig(randInt)
}

func testAccStorageBucketWebsiteConfigWithError(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	bucket = "tf-test-bucket-%[1]d"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

	website {
		index_document = "index.html"
		error_document = "error.html"
	}
}
`, randInt) + testAccCommonIamDependenciesEditorConfig(randInt)
}

func testAccStorageBucketDestroyedConfig(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	bucket = "tf-test-bucket-%[1]d"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key
}
`, randInt) + testAccCommonIamDependenciesEditorConfig(randInt)
}

func testAccStorageBucketConfigWithCORS(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	bucket = "tf-test-bucket-%[1]d"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

	cors_rule {
		allowed_headers = ["*"]
		allowed_methods = ["PUT","POST"]
		allowed_origins = ["https://www.example.com"]
		expose_headers  = ["x-amz-server-side-encryption","ETag"]
		max_age_seconds = 3000
	}
}
`, randInt) + testAccCommonIamDependenciesEditorConfig(randInt)
}

func testAccStorageBucketConfigWithCORSUpdated(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	bucket = "tf-test-bucket-%[1]d"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

	cors_rule {
		allowed_headers = ["*"]
		allowed_methods = ["GET"]
		allowed_origins = ["https://www.example.ru"]
		expose_headers  = ["x-amz-server-side-encryption","ETag"]
		max_age_seconds = 2000
	}
}
`, randInt) + testAccCommonIamDependenciesEditorConfig(randInt)
}

func testAccStorageBucketConfigWithCORSEmptyOrigin(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	bucket = "tf-test-bucket-%[1]d"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

	cors_rule {
		allowed_headers = ["*"]
		allowed_methods = ["PUT","POST"]
		allowed_origins = [""]
		expose_headers = ["x-amz-server-side-encryption","ETag"]
		max_age_seconds = 3000
	}
}
`, randInt) + testAccCommonIamDependenciesEditorConfig(randInt)
}

func testAccStorageBucketConfigWithNamePrefix(randInt int) string {
	return `
resource "yandex_storage_bucket" "test" {
	bucket_prefix = "tf-test-"

	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key
}
` + testAccCommonIamDependenciesEditorConfig(randInt)
}

func testAccStorageBucketConfigWithGeneratedName(randInt int) string {
	return `
resource "yandex_storage_bucket" "test" {
	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key
}
` + testAccCommonIamDependenciesEditorConfig(randInt)
}

func testAccStorageBucketConfigWithGrants(randInt int, userID string) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
  access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
  secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

  bucket = "tf-test-bucket-%d"
  grant {
    id          = "%s"
    type        = "CanonicalUser"
    permissions = ["WRITE", "READ"]
  }
}
`, randInt, userID) + testAccCommonIamDependenciesAdminConfig(randInt)
}

func testAccStorageBucketConfigWithGrantsUpdate(randInt int, userID string) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
  access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
  secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

  bucket = "tf-test-bucket-%d"
  grant {
    id          = "%s"
    type        = "CanonicalUser"
    permissions = ["READ"]
  }
  grant {
    type        = "Group"
    permissions = ["READ"]
    uri         = "http://acs.amazonaws.com/groups/global/AllUsers"
  }
}
`, randInt, userID) + testAccCommonIamDependenciesAdminConfig(randInt)
}

func testAccStorageBucketConfigWithLifecycle(bucketName string) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "bucket" {
  bucket = %[1]q
  acl    = "private"

  lifecycle_rule {
    id      = "id1"
    prefix  = "path1/"
    enabled = true

    expiration {
      days = 365
    }

    transition {
      days          = 30
      storage_class = "STANDARD_IA"
    }
  }

  lifecycle_rule {
    id      = "id2"
    prefix  = "path2/"
    enabled = true

    expiration {
      date = "2016-01-12"
    }
  }

  lifecycle_rule {
    id      = "id3"
    prefix  = "path3/"
    enabled = true

    transition {
      days          = 0
      storage_class = "STANDARD_IA"
    }
  }

}
`, bucketName)
}

func testAccStorageBucketConfigWithVersioningLifecycle(bucketName string) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "bucket" {
  bucket = %[1]q
  acl    = "private"

  versioning {
    enabled = false
  }

  lifecycle_rule {
    id      = "id1"
    prefix  = "path1/"
    enabled = true

    noncurrent_version_expiration {
      days = 365
    }

    noncurrent_version_transition {
      days          = 30
      storage_class = "STANDARD_IA"
    }

  }

  lifecycle_rule {
    id      = "id2"
    prefix  = "path2/"
    enabled = false

    noncurrent_version_expiration {
      days = 365
    }
  }

  lifecycle_rule {
    id      = "id3"
    prefix  = "path3/"
    enabled = true

    noncurrent_version_transition {
      days          = 0
      storage_class = "STANDARD_IA"
    }
  }
}
`, bucketName)
}

func testAccStorageBucketConfigLifecycleRuleExpirationEmptyConfigurationBlock(rName string) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "bucket" {
  bucket = %[1]q

  lifecycle_rule {
    enabled = true
    id      = "id1"

    expiration {}
  }
}
`, rName)
}

func testAccStorageBucketConfigLifecycleRuleAbortIncompleteMultipartUploadDays(rName string) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "bucket" {
  bucket = %[1]q

  lifecycle_rule {
    abort_incomplete_multipart_upload_days = 7
    enabled                                = true
    id                                     = "id1"
  }
}
`, rName)
}

func testAccStorageBucketSSEDefault(keyName string, randInt int) string {
	return fmt.Sprintf(`
resource "yandex_kms_symmetric_key" "key-a" {
  name              = "%s"
  description       = "description for key-a"
  default_algorithm = "AES_128"
  rotation_period   = "24h"

  labels = {
    tf-label    = "tf-label-value-a"
    empty-label = ""
  }
}

resource "yandex_storage_bucket" "test" {
  access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
  secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

  bucket = "tf-test-bucket-%d"
  server_side_encryption_configuration {
    rule {
  	  apply_server_side_encryption_by_default {
	    kms_master_key_id = yandex_kms_symmetric_key.key-a.id
	    sse_algorithm     = "aws:kms"
  	  }
    }
  }
}
`, keyName, randInt) + testAccCommonIamDependenciesAdminConfig(randInt)
}

func testAccStorageBucketBasic(randInt int) string {
	return fmt.Sprintf(`
resource "yandex_storage_bucket" "test" {
	access_key = yandex_iam_service_account_static_access_key.sa-key.access_key
	secret_key = yandex_iam_service_account_static_access_key.sa-key.secret_key

	bucket = "tf-test-bucket-%d"
}
`, randInt) + testAccCommonIamDependenciesAdminConfig(randInt)
}

func testAccCheckStorageBucketUpdateGrantSingle(resourceName string, id string) func(s *terraform.State) error {
	return func(s *terraform.State) error {
		gh := fmt.Sprintf("grant.%v", grantHash(map[string]interface{}{
			"id":   id,
			"type": "CanonicalUser",
			"uri":  "",
			"permissions": schema.NewSet(
				schema.HashString,
				[]interface{}{"READ", "WRITE"},
			),
		}))

		for _, t := range []resource.TestCheckFunc{
			resource.TestCheckResourceAttr(resourceName, gh+".permissions.#", "2"),
			resource.TestCheckResourceAttr(resourceName, gh+".permissions.2931993811", "READ"),
			resource.TestCheckResourceAttr(resourceName, gh+".permissions.2319431919", "WRITE"),
			resource.TestCheckResourceAttr(resourceName, gh+".type", "CanonicalUser"),
		} {
			if err := t(s); err != nil {
				return err
			}
		}
		return nil
	}
}

func testAccCheckStorageBucketUpdateGrantMulti(resourceName string, id string) func(s *terraform.State) error {
	return func(s *terraform.State) error {
		gh1 := fmt.Sprintf("grant.%v", grantHash(map[string]interface{}{
			"id":   id,
			"type": "CanonicalUser",
			"uri":  "",
			"permissions": schema.NewSet(
				schema.HashString,
				[]interface{}{"READ"},
			),
		}))
		gh2 := fmt.Sprintf("grant.%v", grantHash(map[string]interface{}{
			"id":   "",
			"type": "Group",
			"uri":  "http://acs.amazonaws.com/groups/global/AllUsers",
			"permissions": schema.NewSet(
				schema.HashString,
				[]interface{}{"READ"},
			),
		}))
		for _, t := range []resource.TestCheckFunc{
			resource.TestCheckResourceAttr(resourceName, gh1+".permissions.#", "1"),
			resource.TestCheckResourceAttr(resourceName, gh1+".permissions.2931993811", "READ"),
			resource.TestCheckResourceAttr(resourceName, gh1+".type", "CanonicalUser"),
			resource.TestCheckResourceAttr(resourceName, gh2+".permissions.#", "1"),
			resource.TestCheckResourceAttr(resourceName, gh2+".permissions.2931993811", "READ"),
			resource.TestCheckResourceAttr(resourceName, gh2+".type", "Group"),
			resource.TestCheckResourceAttr(resourceName, gh2+".uri", "http://acs.amazonaws.com/groups/global/AllUsers"),
		} {
			if err := t(s); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestAccStorageBucket_LifecycleBasic(t *testing.T) {
	rInt := acctest.RandInt()
	resourceName := "yandex_storage_bucket.test"
	bucketName := acctest.RandomWithPrefix("tf-test-bucket")

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfigWithLifecycle(bucketName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.id", "id1"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.prefix", "path1/"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.expiration.0.days", "365"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.expiration.0.date", ""),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.expiration.0.expired_object_delete_marker", "false"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.transition.0.date", ""),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.transition.0.days", "30"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.transition.0.storage_class", "STANDARD_IA"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.1.id", "id2"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.1.prefix", "path2/"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.1.expiration.0.date", "2016-01-12"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.1.expiration.0.days", "0"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.1.expiration.0.expired_object_delete_marker", "false"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.2.id", "id3"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.2.prefix", "path3/"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.2.transition.0.days", "0"),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"force_destroy", "acl"},
			},
			{
				Config: testAccStorageBucketConfigWithVersioningLifecycle(bucketName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.id", "id1"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.prefix", "path1/"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.enabled", "true"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.noncurrent_version_expiration.0.days", "365"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.noncurrent_version_transition.0.days", "30"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.0.noncurrent_version_transition.0.storage_class", "STANDARD_IA"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.1.id", "id2"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.1.prefix", "path2/"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.1.enabled", "false"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.1.noncurrent_version_expiration.0.days", "365"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.2.id", "id3"),
					resource.TestCheckResourceAttr(resourceName, "lifecycle_rule.2.prefix", "path3/"),
				),
			},
			{
				Config: testAccStorageBucketConfig(rInt),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
				),
			},
		},
	})
}

// Reference: https://github.com/hashicorp/terraform-provider-aws/issues/11420
func TestAccStorageBucket_LifecycleRule_Expiration_EmptyConfigurationBlock(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-acc-test")
	resourceName := "yandex_storage_bucket.bucket"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfigLifecycleRuleExpirationEmptyConfigurationBlock(rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
				),
			},
		},
	})
}

// Reference: https://github.com/hashicorp/terraform-provider-aws/issues/15138
func TestAccStorageBucket_LifecycleRule_AbortIncompleteMultipartUploadDays_NoExpiration(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-acc-test")
	resourceName := "yandex_storage_bucket.bucket"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckStorageBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccStorageBucketConfigLifecycleRuleAbortIncompleteMultipartUploadDays(rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStorageBucketExists(resourceName),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"force_destroy", "acl"},
			},
		},
	})
}

func wrapWithRetries(f resource.TestCheckFunc) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		err := f(s)
		for i := 0; err != nil && i < 6; i++ {
			time.Sleep(time.Second * 20)
			err = f(s)
		}
		return err
	}
}

func ensureBucketDeleted(n string) resource.TestCheckFunc {
	return wrapWithRetries(func(s *terraform.State) error {
		rs := s.RootModule().Resources[n]
		conn, err := getS3ClientByKeys(rs.Primary.Attributes["access_key"], rs.Primary.Attributes["secret_key"],
			testAccProvider.Meta().(*Config))
		if err != nil {
			return err
		}
		return checkBucketDeleted(rs.Primary.ID, conn)
	})
}

func checkBucketDeleted(ID string, conn *s3.S3) error {
	_, err := conn.HeadBucket(&s3.HeadBucketInput{
		Bucket: aws.String(ID),
	})

	if err == nil {
		return fmt.Errorf("expected NoSuchBucket error, got none")
	}

	awsErr, ok := err.(awserr.RequestFailure)

	if !ok {
		return fmt.Errorf("got unexpected error type: %v", err)
	}

	if awsErr.StatusCode() != 404 {
		return fmt.Errorf("expected NotFound error, got: %v", err)
	}

	return nil
}
