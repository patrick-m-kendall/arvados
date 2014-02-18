from setuptools import setup
import subprocess

minor_version = subprocess.check_output(
    ['git', 'log', '--format=format:%ct.%h', '-n1', '.'])

setup(name='arvados-python-client',
      version='0.1.' + minor_version,
      description='Arvados client library',
      author='Arvados',
      author_email='info@arvados.org',
      url="https://github.com/curoverse/arvados.git",
      license='Apache 2.0',
      packages=['arvados'],
      scripts=[
        'bin/arv-get',
        'bin/arv-put',
        'bin/arv-mount',
        ],
      install_requires=[
        'python-gflags',
        'google-api-python-client',
        'httplib2',
        'urllib3',
        'fusepy',
        ],
      zip_safe=False)
